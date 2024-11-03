package mediaopt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type OptimizationResult struct {
	Success bool
	Message string
	Error   error
}

// AudioOptimizationParams contains parameters for audio optimization
type AudioOptimizationParams struct {
	InputFile  string
	OutputFile string
	// Audio specific parameters
	CompressThreshold float64 // dB threshold for compression
	CompressRatio     float64 // compression ratio
	DialogBoost       float64 // dB boost for dialog frequencies
}

// NewDefaultAudioParams creates default audio optimization parameters
func NewDefaultAudioParams(inputFile string) *AudioOptimizationParams {
	// Create output filename with _optimized suffix
	ext := filepath.Ext(inputFile)
	base := inputFile[:len(inputFile)-len(ext)]
	outputFile := base + "_optimized" + ext

	return &AudioOptimizationParams{
		InputFile:         inputFile,
		OutputFile:        outputFile,
		CompressThreshold: -20, // Start compression at -20dB
		CompressRatio:     3,   // 3:1 compression ratio
		DialogBoost:       2,   // 2dB boost for dialog frequencies
	}
}

// OptimizeAudio processes the audio for better dialog clarity on 2.1 systems
func OptimizeAudio(params *AudioOptimizationParams) OptimizationResult {
	// Verify input file exists
	if _, err := os.Stat(params.InputFile); os.IsNotExist(err) {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("input file does not exist: %s", params.InputFile),
		}
	}

	// Construct FFmpeg filter chain
	filterComplex := fmt.Sprintf(
		// Normalize audio first
		"loudnorm=I=-16:TP=-1.5:LRA=11,"+
			// Apply multiband compression focusing on dialog frequencies
			"compand=attacks=0.05:decays=1:points=-90/-90|-70/-70|-60/-60|%f/%f:gain=2,"+
			// Boost dialog frequencies (1-4kHz)
			"equalizer=f=2000:t=h:w=1:g=%f,"+
			// Final limiter to prevent clipping
			"alimiter=level_in=1:level_out=1:limit=1:attack=5:release=50",
		params.CompressThreshold, params.CompressRatio, params.DialogBoost,
	)

	// Construct FFmpeg command
	cmd := exec.Command("ffmpeg",
		"-i", params.InputFile,
		"-c:v", "copy", // Copy video stream without re-encoding
		"-af", filterComplex,
		"-c:a", "aac", // Use AAC codec for audio
		"-b:a", "192k", // Set audio bitrate
		"-y", // Overwrite output file if exists
		params.OutputFile,
	)

	// Execute FFmpeg command
	if output, err := cmd.CombinedOutput(); err != nil {
		return OptimizationResult{
			Success: false,
			Error:   fmt.Errorf("FFmpeg error: %v\nOutput: %s", err, string(output)),
		}
	}

	return OptimizationResult{
		Success: true,
		Message: fmt.Sprintf("Successfully optimized audio for %s", params.InputFile),
	}
}
