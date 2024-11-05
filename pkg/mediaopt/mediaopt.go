package mediaopt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	MemoryLimit string // Memory limit per FFmpeg process
	TempDir     string // Custom temp directory for processing
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
			cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan error)
			go func() {
				done <- cmd.Wait()
			}()
			select {
			case <-done:
				// Process terminated gracefully
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
			}
		}
		delete(activeProcesses.procs, inputFile)
	}
}

// OptimizeMedia processes the media file for better quality and compression
func OptimizeMedia(params *OptimizationParams) OptimizationResult {
	if _, err := os.Stat(params.InputFile); os.IsNotExist(err) {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("input file does not exist: %s", params.InputFile),
		}
	}

	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(params.TempDir, 0755); err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create temp directory: %v", err),
		}
	}

	// Cleanup any existing process for this file
	CleanupProcess(params.InputFile)

	// Get video duration for progress calculation
	duration, err := getDuration(params.InputFile)
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to get media duration: %v", err),
		}
	}

	// Calculate optimal thread count based on file size
	fileSize, err := getFileSize(params.InputFile)
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to get file size: %v", err),
		}
	}

	threads := runtime.NumCPU()
	if fileSize < 10*1024*1024*1024 { // Less than 10GB
		threads = threads / 2
	}

	// Create temp output file
	tempOutput := filepath.Join(params.TempDir, filepath.Base(params.InputFile)+".temp")

	// Create progress file
	progressFile, err := os.CreateTemp("", "ffmpeg-progress-*")
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create progress file: %v", err),
		}
	}
	defer os.Remove(progressFile.Name())
	defer progressFile.Close()

	// Build FFmpeg command with optimized parameters from the bash script
	args := []string{
		"-i", params.InputFile,
		"-map", "0:v:0", "-c:v", "copy",
		"-map", "0:a:0",
		"-c:a", "ac3",
		"-ac", "2",
		"-b:a", "384k",
		"-af", "volume=1.5,dynaudnorm=f=150:g=15:p=0.7,loudnorm=I=-16:TP=-1.5:LRA=11",
		"-metadata:s:a:0", "title=2.1 Optimized",
		"-threads", fmt.Sprintf("%d", threads),
		"-max_memory", params.MemoryLimit,
		"-y",
		"-nostdin",
		"-progress", progressFile.Name(),
		tempOutput,
	}

	cmd := exec.Command("ffmpeg", args...)

	// Set process priority (Windows specific)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00004000, // BELOW_NORMAL_PRIORITY_CLASS
	}

	// Store the command in activeProcesses
	activeProcesses.Lock()
	activeProcesses.procs[params.InputFile] = cmd
	activeProcesses.Unlock()

	// Create pipe for stderr to capture FFmpeg output
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create stderr pipe: %v", err),
		}
	}

	// Start command
	if err := cmd.Start(); err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to start FFmpeg: %v", err),
		}
	}

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
				break
			}

			progress := parseProgress(strings.TrimSpace(line), duration)
			if progress >= 0 && params.OnProgress != nil {
				params.OnProgress(progress)
			}
		}
	}()

	// Capture stderr output
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			fmt.Println("FFmpeg:", scanner.Text())
		}
	}()

	// Wait for completion
	err = cmd.Wait()

	// Clean up process tracking
	activeProcesses.Lock()
	delete(activeProcesses.procs, params.InputFile)
	activeProcesses.Unlock()

	if err != nil {
		// Cleanup temp file on error
		os.Remove(tempOutput)
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("FFmpeg processing failed: %v", err),
		}
	}

	// Move temp file to final destination
	if err := os.Rename(tempOutput, params.OutputFile); err != nil {
		os.Remove(tempOutput)
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to move output file: %v", err),
		}
	}

	return OptimizationResult{
		Success: true,
		Message: fmt.Sprintf("Successfully optimized %s", params.InputFile),
	}
}
