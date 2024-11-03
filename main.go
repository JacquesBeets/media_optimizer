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
	"path/filepath"
	"sync"

	"media_optimizer/pkg/mediaopt"
	"media_optimizer/pkg/rebuild"

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
	Error      string `json:"error,omitempty"`
}

type RebuildResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
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

	w.Header().Set("Content-Type", "application/json")

	// Execute the rebuild process asynchronously
	go func() {
		result := rebuild.ExecuteRebuild()

		if !result.Success {
			log.Printf("Rebuild failed: %v", result.Error)
		} else {
			log.Printf("Rebuild completed: %s", result.Message)
		}
	}()

	// Return immediate response
	response := RebuildResponse{
		Status:  "initiated",
		Message: "Rebuild process has been initiated. Check logs for progress.",
	}

	json.NewEncoder(w).Encode(response)
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

	// Create optimization parameters with default settings
	params := mediaopt.NewDefaultAudioParams(job.SourcePath)

	// Perform optimization
	result := mediaopt.OptimizeAudio(params)

	// Update job status based on result
	activeJobs.Lock()
	if result.Success {
		job.Status = "completed"
		job.Progress = 100
	} else {
		job.Status = "failed"
		job.Error = result.Error.Error()
	}
	activeJobs.Unlock()

	// Log the result
	if result.Success {
		log.Printf("Successfully optimized media: %s", job.SourcePath)
	} else {
		log.Printf("Failed to optimize media: %s, Error: %v", job.SourcePath, result.Error)
	}
}
