#!/bin/bash

echo "Starting rebuild process..."

# Enable systemd debug shell
sudo systemctl enable debug-shell.service || true

# Function to get detailed logs
get_detailed_logs() {
    echo "=== Journal Logs ==="
    sudo journalctl -u media-optimizer.service -n 50 --no-pager
    
    echo -e "\n=== Systemd Debug Logs ==="
    sudo systemctl status media-optimizer.service -l --no-pager
    
    echo -e "\n=== System Journal ==="
    sudo journalctl -xn 50 --no-pager
    
    echo -e "\n=== Process Status ==="
    ps aux | grep media-optimizer
    
    echo -e "\n=== System Load ==="
    uptime
    
    echo -e "\n=== Service Properties ==="
    sudo systemctl show media-optimizer.service
}

# Pull latest changes
echo "Pulling latest changes..."
if ! git pull; then
    echo "Failed to pull latest changes"
    exit 1
fi

# Get initial state
echo "Initial state:"
get_detailed_logs

# Stop service
echo "Stopping service..."
sudo systemctl stop media-optimizer.service || true
sleep 2

echo "State after stop:"
get_detailed_logs

# Build with debug symbols
echo "Building binary with debug info..."
if ! GOOS=linux go build -gcflags="all=-N -l" -o media-optimizer; then
    echo "Failed to build binary"
    exit 1
fi

# Set permissions
echo "Setting permissions..."
sudo chmod 755 media-optimizer

# Start service with debug logging
echo "Starting service with debug logging..."
sudo bash -c 'SYSTEMD_LOG_LEVEL=debug systemctl start media-optimizer.service'
sleep 2

echo "State after start attempt:"
get_detailed_logs

# Check specific failure conditions
echo "Checking specific failure conditions..."
echo "1. Binary existence and permissions:"
ls -l media-optimizer

echo "2. Service file contents:"
sudo cat /etc/systemd/system/media-optimizer.service

echo "3. System journal errors:"
sudo journalctl -p err -n 50 --no-pager

echo "4. Systemd status:"
sudo systemctl status media-optimizer.service -l --no-pager

echo "5. Port availability:"
sudo netstat -tulpn | grep 8080

echo "6. System resources:"
free -m
df -h

echo "Rebuild process completed"
