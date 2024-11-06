package mediaopt

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNewDefaultParams(t *testing.T) {
	inputFile := "test.mp4"
	params := NewDefaultParams(inputFile)

	if params.InputFile != inputFile {
		t.Errorf("Expected input file %s, got %s", inputFile, params.InputFile)
	}

	expectedOutput := "test_optimized.mp4"
	if filepath.Base(params.OutputFile) != expectedOutput {
		t.Errorf("Expected output file %s, got %s", expectedOutput, filepath.Base(params.OutputFile))
	}

	if !filepath.IsAbs(params.TempDir) {
		t.Error("Expected absolute path for temp directory")
	}
}

func TestCleanupProcess(t *testing.T) {
	// Create a test command that sleeps
	cmd := exec.Command("ping", "127.0.0.1", "-n", "10")

	// Add it to active processes
	activeProcesses.Lock()
	activeProcesses.procs["test"] = cmd
	activeProcesses.Unlock()

	// Start the command
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start test command: %v", err)
	}

	// Call cleanup
	CleanupProcess("test")

	// Verify process was cleaned up
	activeProcesses.Lock()
	_, exists := activeProcesses.procs["test"]
	activeProcesses.Unlock()

	if exists {
		t.Error("Process should have been removed from active processes")
	}

	// Verify process was terminated using syscall.Signal
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("Process should have been terminated")
	}
}

func TestOptimizeMedia(t *testing.T) {
	// Skip if running in CI environment
	if os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI environment")
	}

	// Create a temporary test file
	tempDir, err := os.MkdirTemp("", "mediaopt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.mp4")
	if err := os.WriteFile(testFile, []byte("test data"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create scripts directory and test script
	scriptsDir := "scripts"
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("Failed to create scripts directory: %v", err)
	}

	scriptPath := filepath.Join(scriptsDir, "optimize_media.sh")
	testScript := `#!/bin/bash
echo "Processing $1"
exit 1  # Simulate failure for test
`
	if err := os.WriteFile(scriptPath, []byte(testScript), 0755); err != nil {
		t.Fatalf("Failed to create test script: %v", err)
	}
	defer os.Remove(scriptPath)

	params := NewDefaultParams(testFile)
	params.OnProgress = func(progress float64) {
		if progress < 0 || progress > 100 {
			t.Errorf("Invalid progress value: %f", progress)
		}
	}

	result := OptimizeMedia(params)

	// Since we're using a test script that returns failure, we expect an error
	if result.Success {
		t.Error("Expected failure with test script")
	}

	// Test with non-existent file
	params = NewDefaultParams("nonexistent.mp4")
	result = OptimizeMedia(params)
	if result.Success {
		t.Error("Expected failure with non-existent file")
	}
}
