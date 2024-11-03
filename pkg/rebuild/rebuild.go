package rebuild

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	ServiceName = "media-optimizer.service"
	BinaryName  = "media-optimizer"
)

// ServiceManager handles service operations
type ServiceManager struct {
	serviceName string
}

func NewServiceManager(name string) *ServiceManager {
	return &ServiceManager{serviceName: name}
}

func (sm *ServiceManager) execSystemCtl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
	return sm.execSystemCtl("stop", sm.serviceName)
}

func (sm *ServiceManager) start() error {
	log.Printf("Starting %s...", sm.serviceName)
	return sm.execSystemCtl("start", sm.serviceName)
}

func (sm *ServiceManager) waitForStatus(expectedStatus string, timeout time.Duration) error {
	start := time.Now()
	for {
		if time.Since(start) > timeout {
			return fmt.Errorf("timeout waiting for service status: %s", expectedStatus)
		}

		status, err := sm.getStatus()
		if err == nil && status == expectedStatus {
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
}

func NewBuilder(name string) *Builder {
	return &Builder{binaryName: name}
}

func (b *Builder) build() error {
	log.Println("Building binary...")
	cmd := exec.Command("go", "build", "-o", b.binaryName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (b *Builder) setPermissions() error {
	log.Println("Setting binary permissions...")
	return os.Chmod(b.binaryName, 0755)
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
	if os.Getenv("GOOS") == "linux" {
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

		// Start service
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
	}

	result.Success = true
	result.Message = "Rebuild completed successfully!"
	return result
}
