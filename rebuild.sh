#!/bin/bash

# Exit on any error
set -e

echo "Starting rebuild process..."

# Function to check service status
check_service_status() {
    echo "Checking service status..."
    sudo systemctl status media-optimizer.service || true
    echo "Recent service logs:"
    sudo journalctl -u media-optimizer.service -n 50 --no-pager || true
}

# Pull latest changes
echo "Pulling latest changes..."
if ! git pull; then
    echo "Failed to pull latest changes"
    exit 1
fi

# Check initial service status
echo "Initial service state:"
check_service_status

# Stop service with sudo
echo "Stopping media-optimizer service..."
if ! sudo systemctl stop media-optimizer.service; then
    echo "Failed to stop service"
    check_service_status
    exit 1
fi

echo "Service stop command completed, verifying service is stopped..."
if sudo systemctl is-active media-optimizer.service; then
    echo "ERROR: Service is still running after stop command"
    check_service_status
    exit 1
fi

# Build the binary with proper Linux output name
echo "Building binary..."
if ! GOOS=linux go build -o media-optimizer; then
    echo "Failed to build binary"
    sudo systemctl start media-optimizer.service # Try to restart service even if build fails
    exit 1
fi

# Ensure proper permissions and ownership
echo "Setting binary permissions and ownership..."
sudo chown root:root media-optimizer
sudo chmod 755 media-optimizer

# Copy binary to system location (if needed)
echo "Installing binary..."
sudo cp media-optimizer /usr/local/bin/media-optimizer

# Reload systemd to pick up any changes
echo "Reloading systemd..."
sudo systemctl daemon-reload

# Start service with sudo and capture any errors
echo "Starting media-optimizer service..."
if ! sudo systemctl start media-optimizer.service 2>&1; then
    echo "Failed to start service"
    echo "Checking service unit file..."
    sudo systemctl cat media-optimizer.service
    echo "Checking service status after failed start..."
    check_service_status
    exit 1
fi

# Verify service is running
echo "Verifying service started successfully..."
sleep 2  # Give the service a moment to start up
if ! sudo systemctl is-active media-optimizer.service; then
    echo "ERROR: Service failed to start"
    echo "Checking service unit file..."
    sudo systemctl cat media-optimizer.service
    check_service_status
    exit 1
fi

echo "Rebuild completed successfully"

# Final status check
echo "Final service state:"
check_service_status

# Show running processes
echo "Checking running processes..."
ps aux | grep media-optimizer
