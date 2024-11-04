let currentPath = '/';
let selectedPath = null;
let ws = null;
let reconnectAttempts = 0;
const maxReconnectAttempts = 30; // 30 seconds
let reconnectInterval = null;

function initWebSocket() {
    ws = new WebSocket(`ws://${window.location.host}/ws`);
    
    ws.onmessage = function(event) {
        const data = JSON.parse(event.data);
        console.log('WebSocket message received:', data);
        
        if (data.type === 'status' || data.type === 'progress') {
            updateProgress({
                progress: data.progress,
                status: data.status
            });
        }
    };

    ws.onclose = function() {
        // Only attempt to reconnect if we're showing the rebuild modal
        if (document.getElementById('rebuildModal').style.display === 'flex') {
            clearInterval(reconnectInterval);
            reconnectInterval = null;
            attemptReconnect();
        }
    };

    ws.onerror = function(error) {
        console.error('WebSocket error:', error);
    };

    ws.onopen = function() {
        console.log('WebSocket connection established');
    };
}

function stopReconnecting() {
    if (reconnectInterval) {
        clearInterval(reconnectInterval);
        reconnectInterval = null;
    }
    reconnectAttempts = 0;
    document.getElementById('rebuildModal').style.display = 'none';
    document.getElementById('rebuildBtn').disabled = false;
}

function attemptReconnect() {
    if (reconnectAttempts >= maxReconnectAttempts) {
        stopReconnecting();
        alert('Server reconnection failed. Please refresh the page manually.');
        return;
    }

    reconnectAttempts++;
    fetch('/')
        .then(response => {
            if (response.ok) {
                stopReconnecting();
                window.location.reload();
            } else {
                throw new Error('Server not ready');
            }
        })
        .catch(() => {
            if (!reconnectInterval) {
                reconnectInterval = setInterval(attemptReconnect, 1000);
            }
        });
}

function updateProgress(data) {
    const progressContainer = document.querySelector('.progress-container');
    const progressBar = document.querySelector('.progress');
    const status = document.querySelector('.status');

    progressContainer.style.display = 'block';
    progressBar.style.width = `${data.progress}%`;
    
    let statusText = 'Processing...';
    if (data.status === 'completed') {
        statusText = 'Optimization completed successfully!';
    } else if (data.status === 'failed') {
        statusText = `Optimization failed: ${data.error || 'Unknown error'}`;
    } else if (data.status === 'queued') {
        statusText = 'Queued for optimization...';
    }
    
    status.textContent = statusText;
}

async function loadFiles(path) {
    try {
        const response = await fetch('/api/browse', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ path }),
        });
        const files = await response.json();
        displayFiles(files);
        currentPath = path;
        document.querySelector('.current-path').textContent = `Current path: ${currentPath}`;
    } catch (error) {
        console.error('Error loading files:', error);
    }
}

function displayFiles(files) {
    const fileList = document.querySelector('.file-list');
    fileList.innerHTML = '';

    if (currentPath !== '/') {
        const parentItem = document.createElement('li');
        parentItem.className = 'file-item';
        parentItem.innerHTML = '<span class="file-icon">📁</span> ..';
        parentItem.onclick = () => {
            const parentPath = currentPath.split('/').slice(0, -1).join('/') || '/';
            loadFiles(parentPath);
        };
        fileList.appendChild(parentItem);
    }

    files.forEach(file => {
        const item = document.createElement('li');
        item.className = 'file-item';
        item.innerHTML = `<span class="file-icon">${file.isDir ? '📁' : '📄'}</span> ${file.name}`;

        item.onclick = () => {
            if (file.isDir) {
                loadFiles(file.path);
            } else {
                selectedPath = file.path;
                document.querySelectorAll('.file-item').forEach(i => i.classList.remove('selected'));
                item.classList.add('selected');
                document.getElementById('optimizeBtn').disabled = false;
            }
        };

        fileList.appendChild(item);
    });
}

async function optimizeSelected() {
    if (!selectedPath) return;

    try {
        // First send the HTTP request
        const response = await fetch('/api/optimize', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ path: selectedPath }),
        });
        
        if (!response.ok) {
            throw new Error('Failed to start optimization');
        }

        // Then send the WebSocket message
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({
                type: 'optimize',
                data: { path: selectedPath }
            }));
        } else {
            console.error('WebSocket is not connected');
        }

        // Show initial progress state
        document.querySelector('.progress-container').style.display = 'block';
        document.querySelector('.status').textContent = 'Starting optimization...';
        document.querySelector('.progress').style.width = '0%';
    } catch (error) {
        console.error('Error starting optimization:', error);
        document.querySelector('.status').textContent = `Error: ${error.message}`;
    }
}

async function rebuild() {
    const rebuildBtn = document.getElementById('rebuildBtn');
    rebuildBtn.disabled = true;

    // Show the rebuild modal
    document.getElementById('rebuildModal').style.display = 'flex';
    reconnectAttempts = 0;

    try {
        const response = await fetch('/api/rebuild', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            }
        });

        if (!response.ok) {
            throw new Error('Rebuild failed');
        }

        // The server will restart, and the WebSocket will close
        // triggering the reconnection process
    } catch (error) {
        console.error('Error during rebuild:', error);
        alert('Error during rebuild: ' + error.message);
        stopReconnecting();
    }
}

// Initialize
document.addEventListener('DOMContentLoaded', () => {
    initWebSocket();
    loadFiles('/');
    document.getElementById('optimizeBtn').onclick = optimizeSelected;
    document.getElementById('rebuildBtn').onclick = rebuild;
});
