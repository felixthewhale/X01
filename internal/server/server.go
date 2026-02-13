package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"X01/internal/db"
	"X01/internal/logger"
)

//go:embed web/*
var webAssets embed.FS

var (
	replyChan       = make(chan string)
	activeRequest   = false
	activePrompt    = ""
	currentActivity = "Initializing..."
	WakeupChan      = make(chan bool, 1)
	mu              sync.Mutex
)

// Start initializes and runs the background HTTP server
func Start(port int) {
	// Serve static assets from the embedded filesystem
	sub, _ := fs.Sub(webAssets, "web")
	http.Handle("/web/", http.StripPrefix("/web/", http.FileServer(http.FS(sub))))

	http.HandleFunc("/", handleDashboard)
	http.HandleFunc("/push", handlePush)
	http.HandleFunc("/reply", handleReply)
	http.HandleFunc("/status", handleStatus)

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	localIP := getLocalIP()
	
	logger.LogHeader("WEB DASHBOARD")
	logger.LogInfo("Active at: http://localhost:%d", port)
	if localIP != "" {
		logger.LogInfo("Mobile Access: http://%s:%d", localIP, port)
	}

	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			logger.LogError("Web Dashboard failed: %v", err)
		}
	}()
}

// NotifyWakeup signals the main loop or any active sleep tool to wake up
func NotifyWakeup() {
	select {
	case WakeupChan <- true:
	default:
		// Channel already has a pending wakeup signal
	}
}

// SetActivity updates the current activity string displayed on the dashboard
func SetActivity(activity string) {
	mu.Lock()
	currentActivity = activity
	mu.Unlock()
}

// RequestReply sets the server into "blocking" mode for AskUser
func RequestReply(prompt string) chan string {
	mu.Lock()
	activeRequest = true
	activePrompt = prompt
	mu.Unlock()

	// Clear any stale replies
	select {
	case <-replyChan:
	default:
	}

	return replyChan
}

// ClearRequest cancels any active blocking request
func ClearRequest() {
	mu.Lock()
	activeRequest = false
	activePrompt = ""
	mu.Unlock()
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	// Root serves index.html
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data, err := webAssets.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "Asset not found", http.StatusNotFound)
		return
	}

	tmpl, err := template.New("index").Parse(string(data))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

func handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if data.Content == "" {
		http.Error(w, "Content is required", http.StatusBadRequest)
		return
	}

	if err := db.AddPendingMessage(data.Content); err != nil {
		http.Error(w, "DB Error", http.StatusInternalServerError)
		return
	}

	logger.LogInfo("External message received via Web: %s", data.Content)
	NotifyWakeup()
	w.WriteHeader(http.StatusOK)
}

func handleReply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	mu.Lock()
	if !activeRequest {
		mu.Unlock()
		http.Error(w, "No active request to reply to", http.StatusBadRequest)
		return
	}
	mu.Unlock()

	NotifyWakeup()
	replyChan <- data.Content
	w.WriteHeader(http.StatusOK)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	pending, _ := db.GetPendingCount()
	history, _ := db.GetHistory(15)
	
	mu.Lock()
	status := map[string]interface{}{
		"active_request":   activeRequest,
		"prompt":           activePrompt,
		"pending_count":    pending,
		"current_activity": currentActivity,
		"history":          history,
		"timestamp":        time.Now().Unix(),
	}
	mu.Unlock()
	json.NewEncoder(w).Encode(status)
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}
