package mediaopt

import (
	"bufio"
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

// OptimizationParams contains parameters for media optimization
type OptimizationParams struct {
	InputFile   string
	OutputFile  string
	MemoryLimit string
	TempDir     string
	OnProgress  ProgressCallback
}

// activeProcesses tracks running FFmpeg processes
var activeProcesses struct {
	sync.Mutex
	procs map[string]*exec.Cmd
}

func init() {
	activeProcesses.procs = make(map[string]*exec.Cmd)
}

// logError logs error messages with timestamp and details
func logError(format string, v ...interface{}) {
	log.Printf("ERROR: "+format, v...)
}

// logInfo logs informational messages with timestamp
func logInfo(format string, v ...interface{}) {
	log.Printf("INFO: "+format, v...)
}

// getEnglishAudioStream gets the index of the English audio stream
func getEnglishAudioStream(inputFile string) (int, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-select_streams", "a",
		inputFile)

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get audio streams: %v", err)
	}

	// Look for English stream in the output
	outputStr := string(output)
	if strings.Contains(strings.ToLower(outputStr), "language\":\"eng") {
		// Parse the stream index from the output
		re := regexp.MustCompile(`"index":(\d+).*?"language":"eng"`)
		matches := re.FindStringSubmatch(outputStr)
		if len(matches) > 1 {
			index, err := strconv.Atoi(matches[1])
			if err == nil {
				return index, nil
			}
		}
	}

	// If no English stream found, return the first audio stream
	return 0, nil
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

func getDuration(inputFile string) (float64, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
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

	// Get English audio stream index
	audioStreamIndex, err := getEnglishAudioStream(params.InputFile)
	if err != nil {
		logError("Failed to get English audio stream: %v", err)
		audioStreamIndex = 0 // Fallback to first audio stream
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
		"-i", params.InputFile,
		"-map", "0:v:0", "-c:v", "copy",
		"-map", fmt.Sprintf("0:a:%d", audioStreamIndex),
		"-c:a", "ac3",
		"-ac", "2",
		"-b:a", "384k",
		"-af", "volume=1.5,dynaudnorm=f=150:g=15:p=0.7,loudnorm=I=-16:TP=-1.5:LRA=11",
		"-metadata:s:a:0", "title=2.1 Optimized",
		"-metadata:s:a:0", "language=eng",
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
			fmt.Println("FFmpeg:", text)
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
