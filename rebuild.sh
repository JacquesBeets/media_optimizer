#!/bin/bash

# Exit on any error
set -e

echo "Starting rebuild process..."

# Pull latest changes
echo "Pulling latest changes..."
if ! git pull; then
    echo "Failed to pull latest changes"
    exit 1
fi

# Stop service with sudo
echo "Stopping media-optimizer service..."
if ! sudo systemctl stop media-optimizer.service; then
    echo "Failed to stop service"
    exit 1
fi

# Build the binary with proper Linux output name
echo "Building binary..."
if ! GOOS=linux go build -o media-optimizer; then
    echo "Failed to build binary"
    sudo systemctl start media-optimizer.service # Try to restart service even if build fails
    exit 1
fi

# Ensure proper permissions on the binary
echo "Setting binary permissions..."
if ! sudo chmod 755 media-optimizer; then
    echo "Failed to set binary permissions"
    exit 1
fi

# Start service with sudo
echo "Starting media-optimizer service..."
if ! sudo systemctl start media-optimizer.service; then
    echo "Failed to start service"
    exit 1
fi

echo "Rebuild completed successfully"

# Check service status
sudo systemctl status media-optimizer.service
