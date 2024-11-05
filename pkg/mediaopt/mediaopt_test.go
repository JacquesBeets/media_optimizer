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

	if params.MemoryLimit != "4G" {
		t.Errorf("Expected memory limit 4G, got %s", params.MemoryLimit)
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

func TestParseProgress(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		duration float64
		want     float64
	}{
		{
			name:     "valid progress",
			line:     "out_time_ms=5000000",
			duration: 10.0,
			want:     50.0,
		},
		{
			name:     "invalid line",
			line:     "invalid=data",
			duration: 10.0,
			want:     -1,
		},
		{
			name:     "empty line",
			line:     "",
			duration: 10.0,
			want:     -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseProgress(tt.line, tt.duration)
			if got != tt.want {
				t.Errorf("parseProgress() = %v, want %v", got, tt.want)
			}
		})
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

	params := NewDefaultParams(testFile)
	params.OnProgress = func(progress float64) {
		if progress < 0 || progress > 100 {
			t.Errorf("Invalid progress value: %f", progress)
		}
	}

	result := OptimizeMedia(params)

	// Since we can't actually process the fake file, we expect an error
	if result.Success {
		t.Error("Expected failure with fake media file")
	}

	// Test with non-existent file
	params = NewDefaultParams("nonexistent.mp4")
	result = OptimizeMedia(params)
	if result.Success {
		t.Error("Expected failure with non-existent file")
	}
}

func TestGetFileSize(t *testing.T) {
	// Create a temporary test file
	tempFile, err := os.CreateTemp("", "mediaopt_test_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	// Write some test data
	testData := []byte("test data")
	if _, err := tempFile.Write(testData); err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}
	tempFile.Close()

	// Test getFileSize
	size, err := getFileSize(tempFile.Name())
	if err != nil {
		t.Fatalf("getFileSize failed: %v", err)
	}

	if size != int64(len(testData)) {
		t.Errorf("Expected size %d, got %d", len(testData), size)
	}

	// Test with non-existent file
	_, err = getFileSize("nonexistent.txt")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}
