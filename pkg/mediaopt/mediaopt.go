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
	InputFile         string
	OutputFile        string
	CompressThreshold float64
	CompressRatio     float64
	DialogBoost       float64
	VideoQuality      int    // CRF value for x264 (18-28, lower is better quality)
	VideoPreset       string // x264 preset (ultrafast, superfast, veryfast, faster, fast, medium, slow, slower, veryslow)
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

// NewDefaultParams creates default optimization parameters
func NewDefaultParams(inputFile string) *OptimizationParams {
	ext := filepath.Ext(inputFile)
	base := inputFile[:len(inputFile)-len(ext)]
	outputFile := base + "_optimized" + ext

	return &OptimizationParams{
		InputFile:         inputFile,
		OutputFile:        outputFile,
		CompressThreshold: -20,
		CompressRatio:     3,
		DialogBoost:       2,
		VideoQuality:      23,       // Balanced quality (18-28, lower is better)
		VideoPreset:       "faster", // Good balance of speed and quality
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

// hasVideoStream checks if the file has a video stream
func hasVideoStream(inputFile string) (bool, error) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_type",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputFile)

	output, err := cmd.Output()
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(string(output)) == "video", nil
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

// OptimizeMedia processes the media file for better quality and compression
func OptimizeMedia(params *OptimizationParams) OptimizationResult {
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
			Error:   fmt.Errorf("failed to get media duration: %v", err),
		}
	}

	// Check if file has video stream
	hasVideo, err := hasVideoStream(params.InputFile)
	if err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("failed to detect media type: %v", err),
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

	// Construct FFmpeg filter chain for audio
	audioFilter := fmt.Sprintf(
		"loudnorm=I=-16:TP=-1.5:LRA=11,"+
			"compand=attacks=0.05:decays=1:points=-90/-90|-70/-70|-60/-60|%f/%f:gain=2,"+
			"equalizer=f=2000:t=h:w=1:g=%f,"+
			"alimiter=level_in=1:level_out=1:limit=1:attack=5:release=50",
		params.CompressThreshold, params.CompressRatio, params.DialogBoost,
	)

	// Calculate optimal thread count (use 75% of available cores to prevent system lockup)
	threads := int(float64(runtime.NumCPU()) * 0.75)
	if threads < 1 {
		threads = 1
	}

	// Build FFmpeg command based on media type
	var cmd *exec.Cmd
	if hasVideo {
		// Video processing command with keyframe interval for seeking
		cmd = exec.Command("ffmpeg",
			"-i", params.InputFile,
			"-threads", fmt.Sprintf("%d", threads),
			"-c:v", "libx264", // Use x264 codec for video
			"-preset", params.VideoPreset,
			"-crf", fmt.Sprintf("%d", params.VideoQuality),
			"-g", "30", // Keyframe interval for better seeking
			"-sc_threshold", "0", // Disable scene change detection for consistent quality
			"-af", audioFilter,
			"-c:a", "aac",
			"-b:a", "192k",
			"-movflags", "+faststart", // Enable fast start for web playback
			"-progress", progressFile.Name(),
			"-y",
			params.OutputFile,
		)
	} else {
		// Audio-only processing command
		cmd = exec.Command("ffmpeg",
			"-i", params.InputFile,
			"-threads", fmt.Sprintf("%d", threads),
			"-af", audioFilter,
			"-c:a", "aac",
			"-b:a", "192k",
			"-progress", progressFile.Name(),
			"-y",
			params.OutputFile,
		)
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
		Message: fmt.Sprintf("Successfully optimized %s", params.InputFile),
	}
}
