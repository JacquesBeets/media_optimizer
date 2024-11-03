#!/bin/bash

# Exit on any error
set -e

echo "Starting rebuild process..."

# Function to check service status
check_service_status() {
    echo "Checking service status..."
    sudo systemctl status media-optimizer.service || true
}

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

# Ensure proper permissions
echo "Setting binary permissions..."
sudo chmod 755 media-optimizer

# Print working directory and binary location
echo "Current environment:"
pwd
ls -l media-optimizer

# Start service directly first to check for immediate errors
echo "Testing binary directly..."
./media-optimizer &
sleep 2
sudo pkill -f media-optimizer

# Now start with systemd
echo "Starting service with systemd..."
sudo systemctl start media-optimizer.service

# Give it a moment to start
sleep 2

# Check status
check_service_status

# Verify process is running
if ! pgrep -f media-optimizer > /dev/null; then
    echo "ERROR: Process not found. Trying direct start..."
    # If systemd failed, try to run directly to see output
    sudo ./media-optimizer
    exit 1
fi

echo "Rebuild completed successfully"
