package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/gorilla/websocket"
)

//go:embed static/*
var staticFiles embed.FS

type FileInfo struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

type OptimizationJob struct {
	SourcePath string `json:"sourcePath"`
	Status     string `json:"status"`
	Progress   int    `json:"progress"`
}

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for development
		},
	}
	activeJobs = struct {
		sync.RWMutex
		jobs map[string]*OptimizationJob
	}{
		jobs: make(map[string]*OptimizationJob),
	}
)

func main() {
	// Create static file server
	staticContent, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	// Setup routes
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/api/browse", handleBrowse)
	http.HandleFunc("/api/optimize", handleOptimize)
	http.HandleFunc("/api/rebuild", handleRebuild)

	// Start server
	port := 8080
	log.Printf("Server starting on port %d...\n", port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
		log.Fatal(err)
	}
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFS(staticFiles, "static/index.html"))
	tmpl.Execute(w, nil)
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// WebSocket message handling loop
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}

		// Echo the message back for now
		if err := conn.WriteMessage(messageType, message); err != nil {
			log.Printf("WebSocket write error: %v", err)
			break
		}
	}
}

func handleRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Execute the rebuild script
	cmd := exec.Command("cmd", "/C", "rebuild.bat")
	output, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf("Rebuild failed: %v\n%s", err, string(output)), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Rebuild completed successfully. Server will restart.",
	})
}

func handleBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// If no path provided, start from root
	if request.Path == "" {
		request.Path = "/"
	}

	files, err := listFiles(request.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func handleOptimize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Create new optimization job
	job := &OptimizationJob{
		SourcePath: request.Path,
		Status:     "queued",
		Progress:   0,
	}

	// Store job
	activeJobs.Lock()
	activeJobs.jobs[request.Path] = job
	activeJobs.Unlock()

	// Start optimization in background
	go optimizeMedia(job)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "optimization started",
		"path":   request.Path,
	})
}

func listFiles(path string) ([]FileInfo, error) {
	var files []FileInfo

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		files = append(files, FileInfo{
			Name:  entry.Name(),
			Path:  fullPath,
			IsDir: entry.IsDir(),
		})
	}

	return files, nil
}

func optimizeMedia(job *OptimizationJob) {
	// Update job status
	activeJobs.Lock()
	job.Status = "processing"
	activeJobs.Unlock()

	// TODO: Implement actual media optimization using FFmpeg
	// This will include:
	// 1. Video optimization
	// 2. Audio enhancement for 2:1 soundbar
	// 3. Progress updates through WebSocket

	// For now, just mark as completed
	activeJobs.Lock()
	job.Status = "completed"
	job.Progress = 100
	activeJobs.Unlock()
}
