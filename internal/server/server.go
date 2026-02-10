package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sync"
	"time"

	"X01/internal/db"
	"X01/internal/logger"
)

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
	tmpl, err := template.New("dashboard").Parse(dashboardHTML)
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

const dashboardHTML = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>X01 Dashboard</title>
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg: #0a0a0c;
            --card: #141417;
            --card-hover: #1c1c21;
            --accent: #00f2ff;
            --accent-dim: rgba(0, 242, 255, 0.1);
            --accent-glow: rgba(0, 242, 255, 0.3);
            --text: #e0e0e6;
            --text-dim: #80808a;
            --user: #00f2ff;
            --assistant: #bb86fc;
            --tool: #03dac6;
            --danger: #ff4d4d;
        }

        * { box-sizing: border-box; margin: 0; padding: 0; -webkit-tap-highlight-color: transparent; }
        
        body {
            background: var(--bg);
            color: var(--text);
            font-family: 'Outfit', sans-serif;
            display: flex;
            flex-direction: column;
            align-items: center;
            min-height: 100vh;
            padding: 15px;
            overflow-x: hidden;
        }

        .container { width: 100%; max-width: 600px; }

        header {
            text-align: center;
            margin-bottom: 30px;
            padding-top: 10px;
        }

        h1 { font-weight: 600; letter-spacing: 2px; color: var(--accent); text-transform: uppercase; font-size: 1.5rem; }

        .status-badge {
            display: inline-flex;
            align-items: center;
            background: var(--card);
            padding: 8px 16px;
            border-radius: 20px;
            font-size: 0.8rem;
            margin-top: 10px;
            border: 1px solid #222;
        }

        .dot {
            width: 8px;
            height: 8px;
            background: var(--accent);
            border-radius: 50%;
            margin-right: 10px;
            box-shadow: 0 0 10px var(--accent);
            animation: pulse 2s infinite;
        }

        @keyframes pulse {
            0% { transform: scale(1); opacity: 1; }
            50% { transform: scale(1.5); opacity: 0.5; }
            100% { transform: scale(1); opacity: 1; }
        }

        .activity-banner {
            background: var(--accent-dim);
            color: var(--accent);
            padding: 12px;
            border-radius: 12px;
            margin-bottom: 20px;
            font-size: 0.9rem;
            text-align: center;
            border: 1px solid var(--accent-glow);
            font-weight: 600;
        }

        .card {
            background: var(--card);
            border-radius: 20px;
            padding: 20px;
            border: 1px solid #222;
            box-shadow: 0 10px 30px rgba(0,0,0,0.5);
            margin-bottom: 20px;
        }

        .history-section {
            display: flex;
            flex-direction: column;
            height: 350px;
            overflow-y: auto;
            margin-bottom: 20px;
            padding-right: 5px;
            scrollbar-width: thin;
            scrollbar-color: #333 transparent;
        }

        .history-section::-webkit-scrollbar { width: 4px; }
        .history-section::-webkit-scrollbar-thumb { background: #333; border-radius: 10px; }

        .msg {
            margin-bottom: 15px;
            padding: 12px;
            border-radius: 12px;
            font-size: 0.85rem;
            line-height: 1.4;
            position: relative;
            background: rgba(255,255,255,0.03);
            border-left: 3px solid transparent;
        }

        .msg-user { border-left-color: var(--user); }
        .msg-assistant { border-left-color: var(--assistant); }
        .msg-tool { border-left-color: var(--tool); background: rgba(3, 218, 198, 0.05); }

        .msg-role {
            font-size: 0.65rem;
            text-transform: uppercase;
            font-weight: 600;
            margin-bottom: 5px;
            opacity: 0.7;
        }

        .msg-user .msg-role { color: var(--user); }
        .msg-assistant .msg-role { color: var(--assistant); }
        .msg-tool .msg-role { color: var(--tool); }

        .msg-content { white-space: pre-wrap; word-break: break-word; }
        
        .reasoning {
            font-style: italic;
            font-size: 0.8rem;
            color: var(--text-dim);
            margin-top: 8px;
            padding-top: 8px;
            border-top: 1px solid #333;
        }

        .prompt-box {
            background: var(--accent-dim);
            padding: 15px;
            border-radius: 12px;
            border: 1px solid var(--accent);
            margin-bottom: 20px;
            font-size: 0.95rem;
            display: none;
        }

        .prompt-label { font-size: 0.7rem; color: var(--accent); text-transform: uppercase; font-weight: 600; margin-bottom: 8px; }

        textarea {
            width: 100%;
            height: 100px;
            background: #1c1c21;
            border: 1px solid #333;
            border-radius: 12px;
            padding: 15px;
            color: white;
            font-family: inherit;
            resize: none;
            font-size: 1rem;
            transition: border-color 0.3s ease;
            margin-bottom: 15px;
        }

        textarea:focus { outline: none; border-color: var(--accent); }

        button {
            width: 100%;
            background: var(--accent);
            color: var(--bg);
            border: none;
            padding: 16px;
            border-radius: 12px;
            font-weight: 600;
            font-size: 1rem;
            cursor: pointer;
            transition: all 0.3s ease;
            box-shadow: 0 10px 20px var(--accent-glow);
        }

        button:active { transform: scale(0.98); opacity: 0.8; }
        button:disabled { background: #333; color: #555; box-shadow: none; cursor: not-allowed; }

        .toast {
            position: fixed;
            bottom: 30px;
            left: 50%;
            transform: translateX(-50%) translateY(100px);
            background: var(--accent);
            color: var(--bg);
            padding: 12px 24px;
            border-radius: 30px;
            font-weight: 600;
            transition: transform 0.4s cubic-bezier(0.175, 0.885, 0.32, 1.275);
            z-index: 1000;
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>X01 Orbital</h1>
            <div class="status-badge">
                <div class="dot"></div>
                System Active
            </div>
        </header>

        <div id="activity-monitor" class="activity-banner">Initializing telemetry...</div>

        <div class="card">
            <div class="prompt-label">Mission History</div>
            <div id="history" class="history-section">
                <!-- Messages populate here -->
            </div>
        </div>

        <div class="card">
            <div id="prompt-container" class="prompt-box">
                <div class="prompt-label">Awaiting Authorization</div>
                <div id="active-prompt-text"></div>
            </div>

            <textarea id="message-input" placeholder="Transmit instruction..."></textarea>
            <button id="send-btn">Send Packet</button>
        </div>
    </div>

    <div id="toast" class="toast">Packet Sent</div>

    <script>
        let isWaiting = false;
        let lastHistoryHash = "";
        const historyContainer = document.getElementById('history');
        const promptContainer = document.getElementById('prompt-container');
        const promptText = document.getElementById('active-prompt-text');
        const messageInput = document.getElementById('message-input');
        const sendBtn = document.getElementById('send-btn');
        const toast = document.getElementById('toast');
        const activityMonitor = document.getElementById('activity-monitor');

        async function updateStatus() {
            try {
                const res = await fetch('/status');
                const data = await res.json();
                
                activityMonitor.innerText = data.current_activity || "Idle";

                if (data.active_request) {
                    isWaiting = true;
                    promptContainer.style.display = 'block';
                    promptText.innerText = data.prompt;
                    sendBtn.innerText = "Authorize Reply";
                } else {
                    isWaiting = false;
                    promptContainer.style.display = 'none';
                    sendBtn.innerText = "Transmit Packet";
                }

                // Update history if changed
                const historyHash = JSON.stringify(data.history);
                if (historyHash !== lastHistoryHash) {
                    lastHistoryHash = historyHash;
                    renderHistory(data.history);
                }
            } catch (e) {}
        }

        function renderHistory(history) {
            const atBottom = historyContainer.scrollHeight - historyContainer.scrollTop <= historyContainer.clientHeight + 50;
            
            historyContainer.innerHTML = '';
            if (!history) return;

            history.forEach(m => {
                if (m.role === 'system') return;
                
                const div = document.createElement('div');
                div.className = 'msg msg-' + m.role;
                
                let toolName = m.name ? ' (' + m.name + ')' : '';
                div.innerHTML = 
                    '<div class="msg-role">' + m.role + toolName + '</div>' +
                    '<div class="msg-content">' + escapeHtml(m.content) + '</div>' +
                    (m.reasoning ? '<div class="reasoning">Thought: ' + escapeHtml(m.reasoning) + '</div>' : '');
                historyContainer.appendChild(div);
            });

            if (atBottom) {
                historyContainer.scrollTop = historyContainer.scrollHeight;
            }
        }

        function escapeHtml(text) {
            if (!text) return "";
            const div = document.createElement('div');
            div.innerText = text;
            return div.innerHTML;
        }

        sendBtn.onclick = async () => {
            const content = messageInput.value;
            if (!content) return;

            const endpoint = isWaiting ? '/reply' : '/push';
            
            sendBtn.disabled = true;
            try {
                const res = await fetch(endpoint, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ content })
                });

                if (res.ok) {
                    messageInput.value = '';
                    showToast("Signal Transmitted");
                    updateStatus();
                }
            } catch (e) {
                showToast("Link Failure");
            }
            sendBtn.disabled = false;
        };

        function showToast(msg) {
            toast.innerText = msg;
            toast.style.transform = "translateX(-50%) translateY(0)";
            setTimeout(() => {
                toast.style.transform = "translateX(-50%) translateY(100px)";
            }, 3000);
        }

        setInterval(updateStatus, 2000);
        updateStatus();
    </script>
</body>
</html>
`
