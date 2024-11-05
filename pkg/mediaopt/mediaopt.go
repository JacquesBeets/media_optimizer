package mediaopt

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type OptimizationResult struct {
	Success bool
	Message string
	Error   error
}

type ProgressCallback func(float64)

type OptimizationParams struct {
	InputFile   string
	OutputFile  string
	MemoryLimit string
	TempDir     string
	OnProgress  ProgressCallback
}

// StreamInfo represents a media stream in a file
type StreamInfo struct {
	Index     int    `json:"index"`
	CodecType string `json:"codec_type"`
	Language  string `json:"tags,omitempty"`
}

// FFProbeOutput represents the JSON output from ffprobe
type FFProbeOutput struct {
	Streams []StreamInfo `json:"streams"`
}

var (
	activeProcesses struct {
		sync.Mutex
		procs map[string]*exec.Cmd
	}
	logFile *os.File
)

func init() {
	activeProcesses.procs = make(map[string]*exec.Cmd)

	logDir := filepath.Join(os.TempDir(), "ffmpeg_processing")
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "mediaopt.log")
	var err error
	logFile, err = os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open log file: %v", err)
		return
	}
	log.SetOutput(logFile)
}

// sanitizeFilename removes or replaces characters that might cause issues
func sanitizeFilename(filename string) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)

	reg := regexp.MustCompile(`[{}[\]()]+`)
	sanitized := reg.ReplaceAllString(base, "_")

	reg = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	sanitized = reg.ReplaceAllString(sanitized, "_")

	reg = regexp.MustCompile(`_+`)
	sanitized = reg.ReplaceAllString(sanitized, "_")

	ext = strings.ToLower(ext)
	if ext != ".mkv" && ext != ".mp4" && ext != ".avi" {
		ext = ".mkv"
	}

	return sanitized + ext
}

// NewDefaultParams creates default optimization parameters
func NewDefaultParams(inputFile string) *OptimizationParams {
	ext := filepath.Ext(inputFile)
	base := inputFile[:len(inputFile)-len(ext)]
	outputFile := base + "_optimized" + ext
	tempDir := filepath.Join(os.TempDir(), "ffmpeg_processing")

	return &OptimizationParams{
		InputFile:   inputFile,
		OutputFile:  outputFile,
		MemoryLimit: "4G",
		TempDir:     tempDir,
	}
}

// getDuration gets the duration of the input file in seconds
func getDuration(inputFile string) (float64, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-analyzeduration", "100M",
		"-probesize", "100M",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputFile)

	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return 0, err
	}

	return duration, nil
}

// getFileSize gets the size of the file in bytes
func getFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// parseProgress parses FFmpeg progress output
func parseProgress(line string, duration float64) float64 {
	if strings.HasPrefix(line, "out_time_ms=") {
		timeStr := strings.TrimPrefix(line, "out_time_ms=")
		timeMs, err := strconv.ParseInt(timeStr, 10, 64)
		if err != nil {
			return 0
		}
		timeSec := float64(timeMs) / 1000000.0
		return (timeSec / duration) * 100
	}
	return -1
}

// CleanupProcess ensures the FFmpeg process is properly terminated
func CleanupProcess(inputFile string) {
	activeProcesses.Lock()
	defer activeProcesses.Unlock()

	if cmd, exists := activeProcesses.procs[inputFile]; exists {
		if cmd.Process != nil {
			logInfo("Cleaning up process for %s", inputFile)
			cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan error)
			go func() {
				done <- cmd.Wait()
			}()
			select {
			case <-done:
				logInfo("Process terminated gracefully")
			case <-time.After(5 * time.Second):
				logInfo("Process killed after timeout")
				cmd.Process.Kill()
			}
		}
		delete(activeProcesses.procs, inputFile)
	}
}

// Logging functions
func logError(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Printf("ERROR: %s", msg)
	fmt.Printf("ERROR: %s\n", msg)
}

func logInfo(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Printf("INFO: %s", msg)
	fmt.Printf("INFO: %s\n", msg)
}

func logDebug(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Printf("DEBUG: %s", msg)
	fmt.Printf("DEBUG: %s\n", msg)
}

func getEnglishAudioStream(inputFile string) (int, error) {
	logDebug("Detecting audio streams in %s", inputFile)
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-analyzeduration", "100M",
		"-probesize", "100M",
		"-print_format", "json",
		"-show_streams",
		inputFile)

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get streams: %v", err)
	}

	var probeOutput FFProbeOutput
	if err := json.Unmarshal(output, &probeOutput); err != nil {
		return 0, fmt.Errorf("failed to parse ffprobe output: %v", err)
	}

	logInfo("Found %d streams in file", len(probeOutput.Streams))

	// First pass: Look for audio stream with eng language tag
	for _, stream := range probeOutput.Streams {
		if stream.CodecType == "audio" {
			tags := strings.ToLower(stream.Language)
			if strings.Contains(tags, "eng") {
				logInfo("Found English audio stream at index %d", stream.Index)
				return stream.Index, nil
			}
		}
	}

	// Second pass: Look for any audio stream
	for _, stream := range probeOutput.Streams {
		if stream.CodecType == "audio" {
			logInfo("No English audio found, using first audio stream at index %d", stream.Index)
			return stream.Index, nil
		}
	}

	return 0, fmt.Errorf("no suitable audio stream found")
}

func OptimizeMedia(params *OptimizationParams) OptimizationResult {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()

	logInfo("Starting optimization for %s", params.InputFile)
	logInfo("Log file location: %s", filepath.Join(params.TempDir, "mediaopt.log"))

	if _, err := os.Stat(params.InputFile); os.IsNotExist(err) {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("input file does not exist: %s", params.InputFile),
		}
	}

	if err := os.MkdirAll(params.TempDir, 0755); err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create temp directory: %v", err),
		}
	}

	// Detailed file information logging
	fileInfo, err := os.Stat(params.InputFile)
	if err == nil {
		logInfo("Input file details: Size=%d bytes, ModTime=%v",
			fileInfo.Size(), fileInfo.ModTime())
	}

	audioStreamIndex, err := getEnglishAudioStream(params.InputFile)
	if err != nil {
		logError("Failed to get English audio stream: %v", err)
		audioStreamIndex = 0
	}
	logInfo("Using audio stream index: %d", audioStreamIndex)

	duration, err := getDuration(params.InputFile)
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to get media duration: %v", err),
		}
	}
	logInfo("Media duration: %.2f seconds", duration)

	threads := runtime.NumCPU()
	logInfo("Total CPU threads: %d", threads)

	sanitizedName := sanitizeFilename(filepath.Base(params.InputFile))
	tempOutput := filepath.Join(params.TempDir, fmt.Sprintf("temp_%d%s", time.Now().UnixNano(), filepath.Ext(sanitizedName)))
	logDebug("Temporary output file: %s", tempOutput)

	progressFile, err := os.CreateTemp(params.TempDir, "ffmpeg-progress-*.txt")
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create progress file: %v", err),
		}
	}
	defer os.Remove(progressFile.Name())
	defer progressFile.Close()

	reportFile := filepath.Join(params.TempDir, fmt.Sprintf("ffreport_%d.log", time.Now().UnixNano()))
	logDebug("FFmpeg report file: %s", reportFile)

	args := []string{
		"-analyzeduration", "100M",
		"-probesize", "100M",
		"-i", params.InputFile,
		"-map", "0:v:0",
		"-c:v", "copy",
		"-map", fmt.Sprintf("0:a:%d", audioStreamIndex),
		"-c:a", "ac3",
		"-ac", "2",
		"-b:a", "384k",
		"-af", "volume=1.5,dynaudnorm=f=150:g=15:p=0.7,loudnorm=I=-16:TP=-1.5:LRA=11",
		"-metadata:s:a:0", "title=2.1 Optimized",
		"-metadata:s:a:0", "language=eng",
		"-movflags", "+faststart",
		"-threads", fmt.Sprintf("%d", threads),
		"-y",
		"-nostdin",
		"-progress", progressFile.Name(),
		tempOutput,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	logInfo("Executing FFmpeg command: %v", cmd.Args)

	cmd.Env = append(os.Environ(),
		fmt.Sprintf("FFREPORT=file=%s:level=32", reportFile),
		"FFREPORT_LEVEL=32",
	)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create stderr pipe: %v", err),
		}
	}

	// Stderr logging goroutine
	stderrChan := make(chan struct{})
	go func() {
		defer close(stderrChan)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			text := scanner.Text()
			logDebug("FFmpeg stderr: %s", text)

			// Additional error detection
			if strings.Contains(strings.ToLower(text), "error") ||
				strings.Contains(strings.ToLower(text), "fail") {
				logError("Potential FFmpeg error: %s", text)
			}
		}
	}()

	// Progress monitoring
	progressChan := make(chan float64, 1)
	go func() {
		progressFile.Seek(0, 0)
		reader := bufio.NewReader(progressFile)
		lastProgress := 0.0
		for {
			line, err := reader.ReadString('\n')
			if err == io.EOF {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if err != nil {
				logError("Progress reading error: %v", err)
				break
			}

			progress := parseProgress(strings.TrimSpace(line), duration)
			if progress > lastProgress {
				lastProgress = progress
				progressChan <- progress
				logInfo("Progress: %.2f%%", progress)
			}
		}
		close(progressChan)
	}()

	// Start the command
	if err := cmd.Start(); err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to start FFmpeg: %v", err),
		}
	}

	// Wait for completion with progress monitoring
	var finalProgress float64
	for progress := range progressChan {
		finalProgress = progress
		if params.OnProgress != nil {
			params.OnProgress(progress)
		}
	}

	// Wait for stderr logging to complete
	<-stderrChan

	// Check command result
	if err := cmd.Wait(); err != nil {
		logError("FFmpeg process failed: %v", err)
		os.Remove(tempOutput)
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("FFmpeg processing failed: %v", err),
		}
	}

	// Move output file
	if err := os.Rename(tempOutput, params.OutputFile); err != nil {
		logError("Failed to move output file: %v", err)
		os.Remove(tempOutput)
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to move output file: %v", err),
		}
	}

	logInfo("Successfully completed optimization. Final progress: %.2f%%", finalProgress)
	return OptimizationResult{
		Success: true,
		Message: fmt.Sprintf("Successfully optimized %s", params.InputFile),
	}
}
