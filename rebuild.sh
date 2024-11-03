#!/bin/bash

# Exit on any error
set -e

echo "Starting rebuild process..."

# Function to check service status with error capture
check_service_status() {
    echo "Checking service status..."
    sudo systemctl status media-optimizer.service 2>&1 || true
}

# Show service configuration
echo "Current service configuration:"
sudo systemctl cat media-optimizer.service

# Pull latest changes
echo "Pulling latest changes..."
if ! git pull; then
    echo "Failed to pull latest changes"
    exit 1
fi

# Stop service with sudo
echo "Stopping media-optimizer service..."
sudo systemctl stop media-optimizer.service || true

# Build the binary with proper Linux output name
echo "Building binary..."
if ! GOOS=linux go build -o media-optimizer; then
    echo "Failed to build binary"
    exit 1
fi

# Get absolute path
BINARY_PATH="$(pwd)/media-optimizer"

# Ensure proper permissions
echo "Setting binary permissions..."
sudo chmod 755 "$BINARY_PATH"

# Print working directory and binary location
echo "Current environment:"
pwd
ls -l "$BINARY_PATH"

# Start service with full path and error capture
echo "Starting service..."
START_OUTPUT=$(sudo systemctl start media-optimizer.service 2>&1)
START_STATUS=$?

if [ $START_STATUS -ne 0 ]; then
    echo "Failed to start service. Error output:"
    echo "$START_OUTPUT"
    echo "Trying direct execution..."
    sudo "$BINARY_PATH" 2>&1
    exit 1
fi

# Check logs immediately after start
echo "Checking service logs..."
sudo journalctl -u media-optimizer.service -n 50 --no-pager --since "10 seconds ago"

# Give it a moment to start
sleep 2

# Final status check
echo "Final service status:"
check_service_status

echo "Process information:"
ps aux | grep media-optimizer

echo "Port status:"
sudo netstat -tulpn | grep media-optimizer

echo "Rebuild completed successfully"
