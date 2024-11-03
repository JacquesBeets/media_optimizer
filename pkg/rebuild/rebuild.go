package rebuild

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	ServiceName = "media-optimizer.service"
	BinaryName  = "media-optimizer"
	// Add Linux Go binary path
	LinuxGoBinary = "/usr/local/go/bin/go"
)

// ServiceManager handles service operations
type ServiceManager struct {
	serviceName string
	workingDir  string
}

func NewServiceManager(name string) *ServiceManager {
	wd, err := os.Getwd()
	if err != nil {
		log.Printf("Warning: Could not get working directory: %v", err)
		wd = "."
	}
	return &ServiceManager{
		serviceName: name,
		workingDir:  wd,
	}
}

func (sm *ServiceManager) execSystemCtl(args ...string) error {
	log.Printf("Executing systemctl %s", strings.Join(args, " "))
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("systemctl %s failed: %v", strings.Join(args, " "), err)
		return err
	}
	return nil
}

func (sm *ServiceManager) getStatus() (string, error) {
	cmd := exec.Command("systemctl", "is-active", sm.serviceName)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (sm *ServiceManager) stop() error {
	log.Printf("Stopping %s...", sm.serviceName)

	// Force stop the service
	if err := sm.execSystemCtl("stop", "--force", sm.serviceName); err != nil {
		log.Printf("Warning: Force stop failed: %v", err)
	}

	// Kill any remaining processes
	if err := exec.Command("pkill", "-f", BinaryName).Run(); err != nil {
		log.Printf("Warning: pkill failed: %v", err)
	}

	return nil
}

func (sm *ServiceManager) reloadDaemon() error {
	log.Println("Reloading systemd daemon...")
	return sm.execSystemCtl("daemon-reload")
}

func (sm *ServiceManager) resetFailed() error {
	log.Println("Resetting failed state...")
	return sm.execSystemCtl("reset-failed", sm.serviceName)
}

func (sm *ServiceManager) start() error {
	log.Printf("Starting %s...", sm.serviceName)

	// Reset any failed state
	if err := sm.resetFailed(); err != nil {
		log.Printf("Warning: reset-failed error: %v", err)
	}

	// Reload daemon
	if err := sm.reloadDaemon(); err != nil {
		log.Printf("Warning: daemon-reload error: %v", err)
	}

	// Try to start the service
	if err := sm.execSystemCtl("start", sm.serviceName); err != nil {
		// Get detailed status
		cmd := exec.Command("systemctl", "status", sm.serviceName)
		output, _ := cmd.CombinedOutput()
		log.Printf("Service status after failed start:\n%s", string(output))

		// Get journal logs
		cmd = exec.Command("journalctl", "-u", sm.serviceName, "-n", "50", "--no-pager")
		output, _ = cmd.CombinedOutput()
		log.Printf("Recent service logs:\n%s", string(output))
		return err
	}

	return nil
}

func (sm *ServiceManager) waitForStatus(expectedStatus string, timeout time.Duration) error {
	log.Printf("Waiting for service status: %s", expectedStatus)
	start := time.Now()
	for {
		if time.Since(start) > timeout {
			// Get detailed status on timeout
			cmd := exec.Command("systemctl", "status", sm.serviceName)
			output, _ := cmd.CombinedOutput()
			log.Printf("Service status on timeout:\n%s", string(output))

			// Get journal logs
			cmd = exec.Command("journalctl", "-u", sm.serviceName, "-n", "50", "--no-pager")
			output, _ = cmd.CombinedOutput()
			log.Printf("Recent service logs:\n%s", string(output))
			return fmt.Errorf("timeout waiting for service status: %s", expectedStatus)
		}

		status, err := sm.getStatus()
		if err == nil && status == expectedStatus {
			log.Printf("Service reached expected status: %s", expectedStatus)
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}
}

// GitOperations handles git-related tasks
type GitOperations struct{}

func (g *GitOperations) pull() error {
	log.Println("Pulling latest changes...")
	cmd := exec.Command("git", "pull")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (g *GitOperations) checkStatus() error {
	cmd := exec.Command("git", "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	if len(output) > 0 {
		return fmt.Errorf("working directory not clean")
	}
	return nil
}

// Builder handles the build process
type Builder struct {
	binaryName string
	workingDir string
}

func NewBuilder(name string) *Builder {
	wd, err := os.Getwd()
	if err != nil {
		log.Printf("Warning: Could not get working directory: %v", err)
		wd = "."
	}
	return &Builder{
		binaryName: name,
		workingDir: wd,
	}
}

// isLinux checks if we're running on Linux
func isLinux() bool {
	return runtime.GOOS == "linux"
}

// getGoBinary returns the appropriate go binary path
func getGoBinary() string {
	if isLinux() {
		// Try the full path first
		if _, err := os.Stat(LinuxGoBinary); err == nil {
			return LinuxGoBinary
		}
		// Try common Linux paths
		commonPaths := []string{
			"/usr/bin/go",
			"/usr/local/bin/go",
			"/home/linuxbrew/.linuxbrew/bin/go",
		}
		for _, path := range commonPaths {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	return "go" // fallback to PATH-based lookup
}

func (b *Builder) build() error {
	log.Println("Building binary...")

	goBin := getGoBinary()
	log.Printf("Using Go binary: %s", goBin)

	// Use absolute path for output binary
	outputPath := filepath.Join(b.workingDir, b.binaryName)
	log.Printf("Building to: %s", outputPath)

	cmd := exec.Command(goBin, "build", "-o", outputPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (b *Builder) setPermissions() error {
	log.Println("Setting binary permissions...")
	outputPath := filepath.Join(b.workingDir, b.binaryName)
	return os.Chmod(outputPath, 0755)
}

// RebuildResult contains the result of the rebuild operation
type RebuildResult struct {
	Success bool
	Message string
	Error   error
}

// ExecuteRebuild performs the rebuild process
func ExecuteRebuild() RebuildResult {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting rebuild process...")

	result := RebuildResult{Success: false}

	git := &GitOperations{}

	// Check if git working directory is clean
	if err := git.checkStatus(); err != nil {
		result.Error = fmt.Errorf("git working directory check failed: %v", err)
		result.Message = "Git working directory is not clean"
		return result
	}

	// Pull latest changes
	if err := git.pull(); err != nil {
		result.Error = fmt.Errorf("failed to pull changes: %v", err)
		result.Message = "Failed to pull latest changes"
		return result
	}

	// Build binary
	builder := NewBuilder(BinaryName)
	if err := builder.build(); err != nil {
		result.Error = fmt.Errorf("failed to build: %v", err)
		result.Message = "Failed to build binary"
		return result
	}

	// Set permissions
	if err := builder.setPermissions(); err != nil {
		result.Error = fmt.Errorf("failed to set permissions: %v", err)
		result.Message = "Failed to set binary permissions"
		return result
	}

	// If we're on Linux, handle the service
	if isLinux() {
		sm := NewServiceManager(ServiceName)

		// Stop service
		if err := sm.stop(); err != nil {
			result.Error = fmt.Errorf("failed to stop service: %v", err)
			result.Message = "Failed to stop service"
			return result
		}

		// Wait for service to stop
		if err := sm.waitForStatus("inactive", 30*time.Second); err != nil {
			result.Error = fmt.Errorf("service failed to stop: %v", err)
			result.Message = "Service failed to stop"
			return result
		}

		log.Println("Service stopped successfully, preparing for restart...")

		// Force a small delay to ensure the system is ready
		time.Sleep(2 * time.Second)

		// Reset failed state and reload daemon
		if err := sm.resetFailed(); err != nil {
			log.Printf("Warning: Failed to reset failed state: %v", err)
		}

		if err := sm.reloadDaemon(); err != nil {
			log.Printf("Warning: Failed to reload daemon: %v", err)
		}

		// Start the service
		if err := sm.start(); err != nil {
			result.Error = fmt.Errorf("failed to start service: %v", err)
			result.Message = "Failed to start service"
			return result
		}

		// Wait for service to start
		if err := sm.waitForStatus("active", 30*time.Second); err != nil {
			result.Error = fmt.Errorf("service failed to start: %v", err)
			result.Message = "Service failed to start"
			return result
		}

		log.Println("Service successfully restarted and active")
	}

	result.Success = true
	result.Message = "Rebuild completed successfully!"
	return result
}
