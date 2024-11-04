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

// AudioOptimizationParams contains parameters for audio optimization
type AudioOptimizationParams struct {
	InputFile         string
	OutputFile        string
	CompressThreshold float64
	CompressRatio     float64
	DialogBoost       float64
	OnProgress        ProgressCallback
}

// activeProcesses tracks running FFmpeg processes
var activeProcesses struct {
	sync.Mutex
	procs map[string]*exec.Cmd
}

func init() {
	activeProcesses.procs = make(map[string]*exec.Cmd)
}

// NewDefaultAudioParams creates default audio optimization parameters
func NewDefaultAudioParams(inputFile string) *AudioOptimizationParams {
	ext := filepath.Ext(inputFile)
	base := inputFile[:len(inputFile)-len(ext)]
	outputFile := base + "_optimized" + ext

	return &AudioOptimizationParams{
		InputFile:         inputFile,
		OutputFile:        outputFile,
		CompressThreshold: -20,
		CompressRatio:     3,
		DialogBoost:       2,
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
			// Send SIGTERM first for graceful shutdown
			cmd.Process.Signal(syscall.SIGTERM)
			// Wait a bit for graceful shutdown
			done := make(chan error)
			go func() {
				done <- cmd.Wait()
			}()
			select {
			case <-done:
				// Process terminated gracefully
			case <-time.After(5 * time.Second):
				// Force kill if still running
				cmd.Process.Kill()
			}
		}
		delete(activeProcesses.procs, inputFile)
	}
}

// OptimizeAudio processes the audio for better dialog clarity on 2.1 systems
func OptimizeAudio(params *AudioOptimizationParams) OptimizationResult {
	if _, err := os.Stat(params.InputFile); os.IsNotExist(err) {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("input file does not exist: %s", params.InputFile),
		}
	}

	// Cleanup any existing process for this file
	CleanupProcess(params.InputFile)

	// Get video duration for progress calculation
	duration, err := getDuration(params.InputFile)
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to get video duration: %v", err),
		}
	}

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

	// Construct FFmpeg filter chain
	filterComplex := fmt.Sprintf(
		"loudnorm=I=-16:TP=-1.5:LRA=11,"+
			"compand=attacks=0.05:decays=1:points=-90/-90|-70/-70|-60/-60|%f/%f:gain=2,"+
			"equalizer=f=2000:t=h:w=1:g=%f,"+
			"alimiter=level_in=1:level_out=1:limit=1:attack=5:release=50",
		params.CompressThreshold, params.CompressRatio, params.DialogBoost,
	)

	// Calculate optimal thread count (use all available cores)
	threads := runtime.NumCPU()

	// Construct FFmpeg command with thread optimization
	cmd := exec.Command("ffmpeg",
		"-i", params.InputFile,
		"-threads", fmt.Sprintf("%d", threads),
		"-c:v", "copy",
		"-af", filterComplex,
		"-c:a", "aac",
		"-b:a", "192k",
		"-progress", progressFile.Name(),
		"-y",
		params.OutputFile,
	)

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
			// Log FFmpeg output for debugging
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
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("FFmpeg processing failed: %v", err),
		}
	}

	return OptimizationResult{
		Success: true,
		Message: fmt.Sprintf("Successfully optimized audio for %s", params.InputFile),
	}
}
