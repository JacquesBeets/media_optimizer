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

# Function to ensure clean slate
ensure_clean_state() {
    echo "Ensuring clean state..."
    # Kill any existing processes
    sudo pkill -f media-optimizer || true
    sleep 1
    # Double check with force kill if needed
    sudo pkill -9 -f media-optimizer || true
    sleep 1
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
sudo systemctl stop media-optimizer.service || true
ensure_clean_state

# Verify service unit file exists and is valid
echo "Verifying service unit file..."
if ! sudo systemctl cat media-optimizer.service; then
    echo "ERROR: Service unit file is missing or invalid"
    exit 1
fi

# Build the binary with proper Linux output name
echo "Building binary..."
if ! GOOS=linux go build -o media-optimizer; then
    echo "Failed to build binary"
    echo "Build failed, attempting to restart service..."
    sudo systemctl start media-optimizer.service || true
    exit 1
fi

# Verify binary exists and has correct permissions
echo "Verifying binary..."
if [ ! -f "media-optimizer" ]; then
    echo "ERROR: Binary not found after build"
    exit 1
fi

# Ensure proper permissions
echo "Setting binary permissions..."
sudo chmod 755 media-optimizer

# Verify binary is executable
if ! [ -x "media-optimizer" ]; then
    echo "ERROR: Binary is not executable"
    exit 1
fi

# Reload systemd to pick up any changes
echo "Reloading systemd..."
sudo systemctl daemon-reload

# Pre-start verification
echo "Running pre-start verification..."
if ! sudo systemctl show media-optimizer.service --property=LoadState | grep -q "loaded"; then
    echo "ERROR: Service unit is not properly loaded"
    exit 1
fi

# Start service with sudo and capture any errors
echo "Starting media-optimizer service..."
sudo systemctl start media-optimizer.service

# More detailed status check
echo "Checking detailed service status..."
sudo systemctl status media-optimizer.service --no-pager
sudo journalctl -u media-optimizer.service -n 50 --no-pager --since "1 minute ago"

# Verify service is running
echo "Verifying service started successfully..."
sleep 3  # Give the service a moment to start up

# Check if process exists
echo "Checking process existence..."
if ! pgrep -f media-optimizer > /dev/null; then
    echo "ERROR: Process not found"
    check_service_status
    exit 1
fi

# Check service active status
if ! sudo systemctl is-active media-optimizer.service; then
    echo "ERROR: Service not active"
    check_service_status
    exit 1
fi

# Check if service is actually listening on port 8080
echo "Checking if service is listening on port 8080..."
if ! timeout 5 bash -c 'until netstat -tuln | grep ":8080 "; do sleep 1; done'; then
    echo "ERROR: Service is not listening on port 8080"
    check_service_status
    exit 1
fi

echo "Rebuild completed successfully"

# Final status check
echo "Final service state:"
check_service_status

# Show process information
echo "Process information:"
ps aux | grep media-optimizer
