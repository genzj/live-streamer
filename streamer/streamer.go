package streamer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"live-streamer/config"
	"log"
	"math"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Streamer struct {
	videoList         []config.InputItem
	currentVideoIndex int
	cmd               *exec.Cmd
	ctx               context.Context
	cancel            context.CancelFunc
	output            strings.Builder
	manualControl     bool
	mu                sync.Mutex
}

var GlobalStreamer *Streamer

func NewStreamer(videoList []config.InputItem) *Streamer {
	GlobalStreamer = &Streamer{
		videoList:         videoList,
		currentVideoIndex: 0,
		cmd:               nil,
		ctx:               nil,
	}
	return GlobalStreamer
}

func (s *Streamer) start() {
	s.mu.Lock()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	currentVideo := s.videoList[s.currentVideoIndex]
	videoPath := currentVideo.Path
	s.cmd = exec.CommandContext(s.ctx, "ffmpeg", s.buildFFmpegArgs(currentVideo)...)
	s.mu.Unlock()
	s.writeOutput(fmt.Sprintln("start stream: ", videoPath))
	pipe, err := s.cmd.StderrPipe()
	if err != nil {
		log.Printf("failed to get pipe: %v", err)
		return
	}

	reader := bufio.NewReader(pipe)

	if err := s.cmd.Start(); err != nil {
		s.writeOutput(fmt.Sprintf("starting ffmpeg error: %v\n", err))
		return
	}

	go s.log(reader)

	<-s.ctx.Done()
	s.writeOutput(fmt.Sprintf("stop stream: %s\n", videoPath))

	if s.manualControl {
		s.manualControl = false
	} else {
		// stream next video
		s.currentVideoIndex++
		if s.currentVideoIndex >= len(s.videoList) {
			s.currentVideoIndex = 0
		}
	}
}

func (s *Streamer) Stream() {
	for {
		if len(s.videoList) == 0 {
			time.Sleep(time.Second)
			continue
		}
		s.start()
	}
}

func (s *Streamer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		stopped := make(chan error)
		go func() {
			if s.cmd != nil {
				stopped <- s.cmd.Wait()
			}
		}()
		s.cancel()
		if s.cmd != nil && s.cmd.Process != nil {
			select {
			case <-stopped:
				break
			case <-time.After(3 * time.Second):
				_ = s.cmd.Process.Kill()
				break
			}
			s.cmd = nil
		}
	}
}

func (s *Streamer) writeOutput(str string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output.WriteString(str)
}

func (s *Streamer) Add(videoPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.videoList = append(s.videoList, config.InputItem{Path: videoPath})
}

func (s *Streamer) Remove(videoPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.videoList {
		if item.Path == videoPath {
			s.videoList = append(s.videoList[:i], s.videoList[i+1:]...)
			if s.currentVideoIndex >= len(s.videoList) {
				s.currentVideoIndex = 0
			}
			if s.currentVideoIndex == i {
				s.Stop()
			}
			break
		}
	}
}

func (s *Streamer) Prev() {
	s.mu.Lock()
	s.manualControl = true
	s.currentVideoIndex--
	if s.currentVideoIndex < 0 {
		s.currentVideoIndex = len(s.videoList) - 1
	}
	s.mu.Unlock()
	s.Stop()
}

func (s *Streamer) Next() {
	s.mu.Lock()
	s.manualControl = true
	s.currentVideoIndex++
	if s.currentVideoIndex >= len(s.videoList) {
		s.currentVideoIndex = 0
	}
	s.mu.Unlock()
	s.Stop()
}

func (s *Streamer) log(reader *bufio.Reader) {
	select {
	case <-s.ctx.Done():
		return
	default:
		if !config.GlobalConfig.Log.PlayState {
			return
		}
		buf := make([]byte, 1024)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				videoPath, _ := s.GetCurrentVideoPath()
				buf = append([]byte(videoPath), buf...)
				s.writeOutput(string(buf[:n+len(videoPath)]))
			}
			if err != nil {
				if err != io.EOF {
					s.writeOutput(fmt.Sprintf("reading ffmpeg output error: %v\n", err))
				}
				break
			}
		}
	}
}

func (s *Streamer) GetCurrentVideoPath() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.videoList) == 0 {
		return "", errors.New("no video streaming")
	}
	return s.videoList[s.currentVideoIndex].Path, nil
}

func (s *Streamer) GetVideoList() []config.InputItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.videoList
}

func (s *Streamer) GetVideoListPath() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var videoList []string
	for _, item := range s.videoList {
		videoList = append(videoList, item.Path)
	}
	return videoList
}

func (s *Streamer) GetCurrentIndex() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentVideoIndex
}

func (s *Streamer) GetOutput() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output.String()
}

func (s *Streamer) TruncateOutput() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	currentSize := s.output.Len()
	if currentSize > math.MaxInt {
		newStart := currentSize - math.MaxInt
		trimmedOutput := s.output.String()[newStart:]
		s.output.Reset()
		s.output.WriteString(trimmedOutput)
	}
	return currentSize
}

func (s *Streamer) Close() {
	s.Stop()
}

func (s *Streamer) buildFFmpegArgs(videoItem config.InputItem) []string {
	videoPath := videoItem.Path

	args := []string{"-re"}
	if videoItem.Start != "" {
		args = append(args, "-ss", videoItem.Start)
	}

	args = append(args, "-i", videoPath)

	if videoItem.End != "" {
		args = append(args, "-to", videoItem.End)
	}

	args = append(args,
		"-c:v", config.GlobalConfig.Play.VideoCodec,
		"-preset", config.GlobalConfig.Play.Preset,
		"-crf", fmt.Sprintf("%d", config.GlobalConfig.Play.CRF),
		"-maxrate", config.GlobalConfig.Play.MaxRate,
		"-bufsize", config.GlobalConfig.Play.BufSize,
		"-vf", fmt.Sprintf("scale=%s", config.GlobalConfig.Play.Scale),
		"-r", fmt.Sprintf("%d", config.GlobalConfig.Play.FrameRate),
		"-c:a", config.GlobalConfig.Play.AudioCodec,
		"-b:a", config.GlobalConfig.Play.AudioBitrate,
		"-ar", fmt.Sprintf("%d", config.GlobalConfig.Play.AudioSampleRate),
		"-f", config.GlobalConfig.Play.OutputFormat,
		"-stats", "-loglevel", "info",
	)

	if config.GlobalConfig.Play.CustomArgs != "" {
		customArgs := strings.Fields(config.GlobalConfig.Play.CustomArgs)
		args = append(args, customArgs...)
	}

	args = append(args, fmt.Sprintf("%s/%s", config.GlobalConfig.Output.RTMPServer, config.GlobalConfig.Output.StreamKey))

	// logger.GlobalLogger.Println("ffmpeg args: ", args)

	return args
}
