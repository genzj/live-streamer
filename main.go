package main

import (
	"bufio"
	"live-streamer/config"
	"live-streamer/streamer"
	"live-streamer/utils"
	"log"
	"os"
	"strings"

	"github.com/fsnotify/fsnotify"
)

var GlobalStreamer *streamer.Streamer

func main() {
	if !utils.HasFFMPEG() {
		log.Fatal("ffmpeg not found")
	}
	GlobalStreamer = streamer.NewStreamer(config.GlobalConfig.PlayList)
	go input()
	go startWatcher()
	GlobalStreamer.Stream()
	GlobalStreamer.Close()
}

func input() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch scanner.Text() {
		case "prev":
			GlobalStreamer.Prev()
		case "next":
			GlobalStreamer.Next()
		case "quit":
			GlobalStreamer.Close()
			os.Exit(0)
		case "list":
			list := GlobalStreamer.GetVideoListPath()
			log.Println("\nvideo list:\n", strings.Join(list, "\n"))
		case "current":
			log.Println("current video: ", GlobalStreamer.GetCurrentVideo())
		default:
			log.Println("unknown command")
		}
	}
}

func startWatcher() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	for _, item := range config.GlobalConfig.PlayList {
		if item.ItemType == "dir" {
			err = watcher.Add(item.Path)
			if err != nil {
				log.Fatalf("failed to add dir to watcher: %v", err)
			}
			log.Println("watching dir:", item.Path)
		}
	}
	if err != nil {
		log.Fatalf("failed to start watcher: %v", err)
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create {
				if utils.IsSupportedVideo(event.Name) {
					log.Println("new video added:", event.Name)
					GlobalStreamer.Add(event.Name)
				}
			}
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				log.Println("video removed:", event.Name)
				GlobalStreamer.Remove(event.Name)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Println("watcher error:", err)
		}
	}
}
