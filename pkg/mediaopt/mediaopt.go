package mediaopt

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type OptimizationResult struct {
	Success bool
	Message string
	Error   error
}

type ProgressCallback func(float64)

type OptimizationParams struct {
	InputFile  string
	OutputFile string
	TempDir    string
	OnProgress ProgressCallback
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

// NewDefaultParams creates default optimization parameters
func NewDefaultParams(inputFile string) *OptimizationParams {
	ext := filepath.Ext(inputFile)
	base := inputFile[:len(inputFile)-len(ext)]
	outputFile := base + "_optimized" + ext
	tempDir := filepath.Join(os.TempDir(), "ffmpeg_processing")

	return &OptimizationParams{
		InputFile:  inputFile,
		OutputFile: outputFile,
		TempDir:    tempDir,
	}
}

// CleanupProcess ensures the script process is properly terminated
func CleanupProcess(inputFile string) {
	activeProcesses.Lock()
	defer activeProcesses.Unlock()

	if cmd, exists := activeProcesses.procs[inputFile]; exists {
		if cmd.Process != nil {
			logInfo("Cleaning up process for %s", inputFile)
			// Kill the process group to ensure all child processes are terminated
			if pgid, err := os.FindProcess(-cmd.Process.Pid); err == nil {
				pgid.Kill()
			}
			cmd.Process.Kill()
			// Wait for process to finish
			cmd.Wait()
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

func OptimizeMedia(params *OptimizationParams) OptimizationResult {
	logInfo("Starting optimization for %s", params.InputFile)
	logInfo("Log file location: %s", filepath.Join(params.TempDir, "mediaopt.log"))

	if _, err := os.Stat(params.InputFile); os.IsNotExist(err) {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("input file does not exist: %s", params.InputFile),
		}
	}

	// Ensure the scripts directory exists and the script is executable
	scriptPath := filepath.Join("scripts", "optimize_media.sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("optimization script not found: %s", scriptPath),
		}
	}

	// Make script executable
	if err := os.Chmod(scriptPath, 0755); err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to make script executable: %v", err),
		}
	}

	// Create temp directory if it doesn't exist
	if err := os.MkdirAll(params.TempDir, 0755); err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create temp directory: %v", err),
		}
	}

	// Execute the optimization script
	cmd := exec.Command("/bin/bash", scriptPath, params.InputFile)

	// Track the process
	activeProcesses.Lock()
	activeProcesses.procs[params.InputFile] = cmd
	activeProcesses.Unlock()

	// Clean up when done
	defer func() {
		activeProcesses.Lock()
		delete(activeProcesses.procs, params.InputFile)
		activeProcesses.Unlock()
	}()

	// Capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create stdout pipe: %v", err),
		}
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to create stderr pipe: %v", err),
		}
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to start optimization script: %v", err),
		}
	}

	// Create channels for monitoring
	doneChan := make(chan struct{})
	progressChan := make(chan float64)

	// Monitor stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		var totalDuration float64
		for scanner.Scan() {
			text := scanner.Text()
			logInfo("Script output: %s", text)
			if strings.HasPrefix(text, "total_duration=") {
				durationStr := strings.TrimPrefix(text, "total_duration=")
				totalDuration, _ = strconv.ParseFloat(durationStr, 64)
			}
			if strings.HasPrefix(text, "out_time_ms=") && totalDuration > 0 {
				timeStr := strings.TrimPrefix(text, "out_time_ms=")
				timeMs, _ := strconv.ParseInt(timeStr, 10, 64)
				timeSec := float64(timeMs) / 1000000.0
				progress := (timeSec / totalDuration) * 100
				progressChan <- progress
			}
		}
	}()

	// Monitor stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logError("Script error: %s", scanner.Text())
		}
	}()

	// Monitor progress if callback is provided
	if params.OnProgress != nil {
		go func() {
			for {
				select {
				case progress := <-progressChan:
					params.OnProgress(progress)
				case <-doneChan:
					return
				}
			}
		}()
	}

	// Wait for completion
	err = cmd.Wait()
	close(doneChan)

	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("optimization failed: %v", err),
		}
	}

	// Check if output file exists
	expectedOutput := strings.TrimSuffix(params.InputFile, filepath.Ext(params.InputFile)) + "_optimized" + filepath.Ext(params.InputFile)
	if _, err := os.Stat(expectedOutput); os.IsNotExist(err) {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("output file was not created: %s", expectedOutput),
		}
	}

	return OptimizationResult{
		Success: true,
		Message: fmt.Sprintf("Successfully optimized %s", params.InputFile),
	}
}
