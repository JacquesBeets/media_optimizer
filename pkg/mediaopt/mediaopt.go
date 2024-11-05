package mediaopt

import (
	"bufio"
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

var (
	activeProcesses struct {
		sync.Mutex
		procs map[string]*exec.Cmd
	}
	logFile *os.File
)

func init() {
	activeProcesses.procs = make(map[string]*exec.Cmd)

	// Set up logging to file in temp directory
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

type StreamInfo struct {
	Index     int    `json:"index"`
	CodecType string `json:"codec_type"`
	Language  string `json:"tags,omitempty"`
}

type FFProbeOutput struct {
	Streams []StreamInfo `json:"streams"`
}

func logError(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Printf("ERROR: %s", msg)
	fmt.Printf("ERROR: %s\n", msg) // Also print to stdout
}

func logInfo(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Printf("INFO: %s", msg)
	fmt.Printf("INFO: %s\n", msg) // Also print to stdout
}

func getEnglishAudioStream(inputFile string) (int, error) {
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

func getFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

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

func setPriority(pid int) {
	niceCmd := exec.Command("nice", "-n", "10", "--", strconv.Itoa(pid))
	if err := niceCmd.Run(); err != nil {
		logError("Failed to set nice level: %v", err)
	}

	ioniceCmd := exec.Command("ionice", "-c", "2", "-n", "7", "-p", strconv.Itoa(pid))
	if err := ioniceCmd.Run(); err != nil {
		logError("Failed to set I/O priority: %v", err)
	}
}

func OptimizeMedia(params *OptimizationParams) OptimizationResult {
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

	CleanupProcess(params.InputFile)

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

	fileSize, err := getFileSize(params.InputFile)
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to get file size: %v", err),
		}
	}

	threads := runtime.NumCPU()
	if fileSize < 10*1024*1024*1024 {
		threads = threads / 2
	}
	logInfo("Using %d threads for processing", threads)

	sanitizedName := sanitizeFilename(filepath.Base(params.InputFile))
	tempOutput := filepath.Join(params.TempDir, fmt.Sprintf("temp_%d%s", time.Now().UnixNano(), filepath.Ext(sanitizedName)))

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

	cmd := exec.Command("ffmpeg", args...)
	logInfo("Executing FFmpeg command: %v", cmd.Args)

	cmd.Env = append(os.Environ(), fmt.Sprintf("FFREPORT=file=%s:level=32", reportFile))

	activeProcesses.Lock()
	activeProcesses.procs[params.InputFile] = cmd
	activeProcesses.Unlock()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create stderr pipe: %v", err),
		}
	}

	if err := cmd.Start(); err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to start FFmpeg: %v", err),
		}
	}

	if cmd.Process != nil {
		setPriority(cmd.Process.Pid)
		logInfo("Process started with PID: %d", cmd.Process.Pid)
	}

	lastProgress := 0.0
	lastProgressTime := time.Now()
	progressStallTimeout := 5 * time.Minute

	// Monitor progress file
	go func() {
		progressFile.Seek(0, 0)
		reader := bufio.NewReader(progressFile)
		for {
			line, err := reader.ReadString('\n')
			if err == io.EOF {
				if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
					break
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if err != nil {
				logError("Error reading progress: %v", err)
				break
			}

			progress := parseProgress(strings.TrimSpace(line), duration)
			if progress >= 0 {
				if progress > lastProgress {
					lastProgress = progress
					lastProgressTime = time.Now()
					logInfo("Progress: %.2f%%", progress)
				} else if time.Since(lastProgressTime) > progressStallTimeout {
					logError("Progress stalled for more than %v minutes", progressStallTimeout.Minutes())
					cmd.Process.Signal(syscall.SIGTERM)
					break
				}
				if params.OnProgress != nil {
					params.OnProgress(progress)
				}
			}
		}
	}()

	// Capture stderr output
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			text := scanner.Text()
			logInfo("FFmpeg: %s", text)
			if strings.Contains(strings.ToLower(text), "error") {
				logError("FFmpeg error: %s", text)
			}
		}
	}()

	err = cmd.Wait()

	activeProcesses.Lock()
	delete(activeProcesses.procs, params.InputFile)
	activeProcesses.Unlock()

	if err != nil {
		logError("FFmpeg process failed: %v", err)
		os.Remove(tempOutput)
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("FFmpeg processing failed: %v", err),
		}
	}

	if err := os.Rename(tempOutput, params.OutputFile); err != nil {
		logError("Failed to move output file: %v", err)
		os.Remove(tempOutput)
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to move output file: %v", err),
		}
	}

	os.Remove(reportFile)
	logInfo("Successfully completed optimization for %s", params.InputFile)

	return OptimizationResult{
		Success: true,
		Message: fmt.Sprintf("Successfully optimized %s", params.InputFile),
	}
}
