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
	WSConn     *websocket.Conn
}

type RebuildResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

type WSMessage struct {
	Type     string      `json:"type"`
	JobID    string      `json:"jobId"`
	Status   string      `json:"status,omitempty"`
	Progress float64     `json:"progress,omitempty"`
	Error    string      `json:"error,omitempty"`
	Data     interface{} `json:"data,omitempty"`
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
	staticContent, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/api/browse", handleBrowse)
	http.HandleFunc("/api/optimize", handleOptimize)
	http.HandleFunc("/api/rebuild", handleRebuild)

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

	// Handle incoming messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		// Parse the incoming message
		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Failed to parse WebSocket message: %v", err)
			continue
		}

		// Handle different message types
		switch msg.Type {
		case "optimize":
			handleOptimizationRequest(conn, msg.Data.(map[string]interface{})["path"].(string))
		}
	}
}

func handleOptimizationRequest(conn *websocket.Conn, path string) {
	// Create new optimization job
	job := &OptimizationJob{
		SourcePath: path,
		Status:     "queued",
		Progress:   0,
		WSConn:     conn,
	}

	// Store job
	activeJobs.Lock()
	activeJobs.jobs[path] = job
	activeJobs.Unlock()

	// Start optimization in background
	go optimizeMedia(job)

	// Send initial status
	sendWSUpdate(job, "status", 0)
}

func sendWSUpdate(job *OptimizationJob, msgType string, progress float64) {
	if job.WSConn == nil {
		return
	}

	msg := WSMessage{
		Type:     msgType,
		JobID:    job.SourcePath,
		Status:   job.Status,
		Progress: progress,
		Error:    job.Error,
	}

	if err := job.WSConn.WriteJSON(msg); err != nil {
		log.Printf("WebSocket write error: %v", err)
	}
}

func handleRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	go func() {
		result := rebuild.ExecuteRebuild()

		if !result.Success {
			log.Printf("Rebuild failed: %v", result.Error)
		} else {
			log.Printf("Rebuild completed: %s", result.Message)
		}
	}()

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

	// Return success response for the HTTP request
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "optimization initiated",
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
	sendWSUpdate(job, "status", 0)

	// Create optimization parameters with progress callback
	params := mediaopt.NewDefaultAudioParams(job.SourcePath)
	params.OnProgress = func(progress float64) {
		activeJobs.Lock()
		job.Progress = int(progress)
		activeJobs.Unlock()
		sendWSUpdate(job, "progress", progress)
	}

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

	// Final status update
	sendWSUpdate(job, "status", float64(job.Progress))

	// Log the result
	if result.Success {
		log.Printf("Successfully optimized media: %s", job.SourcePath)
	} else {
		log.Printf("Failed to optimize media: %s, Error: %v", job.SourcePath, result.Error)
	}
}
