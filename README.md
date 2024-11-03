# Media Optimizer Server

A Go-based media optimization server that processes media files using FFmpeg, with a web interface for file management.

## Setting up in LXC Container

### 1. Create and Configure LXC Container

```bash
# Create a new container (Ubuntu 22.04 recommended)
lxc launch ubuntu:22.04 media-optimizer

# Enter the container
lxc exec media-optimizer -- bash
```

### 2. Install Go 1.21.6

```bash
# Update package list
apt update
apt upgrade -y

# Install build essentials
apt install -y wget git build-essential

# Download Go 1.21.6
wget https://go.dev/dl/go1.21.6.linux-amd64.tar.gz

# Extract Go to /usr/local
rm -rf /usr/local/go
tar -C /usr/local -xzf go1.21.6.linux-amd64.tar.gz

# Set up Go environment
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
echo 'export GOPATH=$HOME/go' >> ~/.bashrc
source ~/.bashrc

# Verify Go installation
go version
```

### 3. Install FFmpeg

```bash
# Install FFmpeg and dependencies
apt install -y ffmpeg

# Verify FFmpeg installation
ffmpeg -version
```

### 4. Project Setup

```bash
# Create project directory
mkdir -p ~/media-optimizer
cd ~/media-optimizer

# Clone the repository (if using git)
git clone https://github.com/JacquesBeets/media_optimizer.git .

# Or copy the project files manually
# Make sure to include:
# - main.go
# - go.mod
# - go.sum
# - static/
# - rebuild.sh

# Install Go dependencies
go mod download

# Make rebuild script executable
chmod +x rebuild.sh
```

### 5. Building and Running

```bash
# Build the project
go build -o media-optimizer

# Run the server
./media-optimizer
```

The server will start on port 8080. You can access the web interface at `http://<container-ip>:8080`

### 6. Setting up Automatic Start on Container Restart

Create a systemd service file to manage the media optimizer server:

```bash
# Create the service file
cat > /etc/systemd/system/media-optimizer.service << 'EOL'
[Unit]
Description=Media Optimizer Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/root/media-optimizer
ExecStart=/root/media-optimizer/media-optimizer
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOL

# Reload systemd daemon
systemctl daemon-reload

# Enable the service to start on boot
systemctl enable media-optimizer

# Start the service
systemctl start media-optimizer

# Check service status
systemctl status media-optimizer
```

You can manage the service using standard systemd commands:
```bash
# Stop the service
systemctl stop media-optimizer

# Restart the service
systemctl restart media-optimizer

# View logs
journalctl -u media-optimizer -f
```

## Container Network Configuration (optional)

To access the server from outside the container, you'll need to either:

1. Configure port forwarding:
```bash
# From the host machine
lxc config device add media-optimizer webport proxy listen=tcp:<host-ip>:8080 connect=tcp:127.0.0.1:8080
```

2. Or use container's IP directly:
```bash
# Get container's IP
lxc list media-optimizer -f csv -c 4
```

## Development

- The server uses embedded static files from the `static/` directory
- WebSocket is configured to accept connections from any origin (suitable for development)
- Use `rebuild.sh` (Linux) or `rebuild.bat` (Windows) to rebuild the server during development

### Handling Git File Mode Issues

If you encounter issues with Git detecting file mode changes when making rebuild.sh executable, follow these steps on the server:

```bash
# Configure Git to ignore file mode changes
git config core.fileMode false

# Make the script executable
chmod +x rebuild.sh

# Tell Git to ignore changes to rebuild.sh
git update-index --skip-worktree rebuild.sh
```

These commands will prevent Git from tracking executable bit changes and allow smooth git pull operations without conflicts from file mode changes.

## Requirements

- Go 1.21.6 or later
- FFmpeg
- gorilla/websocket (installed via Go modules)

## Notes

- The server embeds static files, so any changes to the frontend require rebuilding the server
- FFmpeg is required for media optimization features
- The server listens on port 8080 by default
- The systemd service ensures the server automatically starts after container restarts
