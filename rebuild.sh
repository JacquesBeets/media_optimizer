#!/bin/bash

# Exit on any error
set -e

echo "Starting rebuild process..."

# Function to check service status with error capture
check_service_status() {
    echo "Checking service status..."
    sudo systemctl status media-optimizer.service 2>&1 || true
}

# Verify service file exists
if [ ! -f "/etc/systemd/system/media-optimizer.service" ]; then
    echo "ERROR: Service file missing. Creating default service file..."
    # Create service file if missing
    sudo tee /etc/systemd/system/media-optimizer.service > /dev/null << EOL
[Unit]
Description=Media Optimizer Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/root/media-optimizer
ExecStart=/root/media-optimizer/media-optimizer
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOL
fi

# Show current service file
echo "Current service file contents:"
sudo cat /etc/systemd/system/media-optimizer.service

# Pull latest changes
echo "Pulling latest changes..."
if ! git pull; then
    echo "Failed to pull latest changes"
    exit 1
fi

# Stop service and wait
echo "Stopping service..."
sudo systemctl stop media-optimizer.service || true
echo "Waiting for service to fully stop..."
sleep 3

# Build the binary with proper Linux output name
echo "Building binary..."
if ! GOOS=linux go build -o media-optimizer; then
    echo "Failed to build binary"
    exit 1
fi

# Ensure proper permissions
echo "Setting binary permissions..."
sudo chmod 755 media-optimizer

# Restart systemd itself
echo "Restarting systemd daemon..."
sudo systemctl daemon-reload

# Enable service if not enabled
echo "Enabling service..."
sudo systemctl enable media-optimizer.service || true

# Start service with status check
echo "Starting service..."
if ! sudo systemctl start media-optimizer.service; then
    echo "Failed to start service. Checking status..."
    check_service_status
    echo "Checking logs..."
    sudo journalctl -u media-optimizer.service -n 50 --no-pager --since "10 seconds ago"
    echo "Trying direct execution for debugging..."
    sudo ./media-optimizer
    exit 1
fi

# Verify service is enabled and running
echo "Verifying service state..."
sudo systemctl is-enabled media-optimizer.service
sudo systemctl is-active media-optimizer.service

# Show running processes
echo "Process information:"
ps aux | grep media-optimizer

echo "Rebuild completed successfully"

# Final status
check_service_status
