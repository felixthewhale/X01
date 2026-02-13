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
        
        activityMonitor.innerText = data.current_activity || "IDLE";

        if (data.active_request) {
            isWaiting = true;
            promptContainer.style.display = 'block';
            promptText.innerText = data.prompt;
            sendBtn.innerText = "AUTHORIZE REPLY";
        } else {
            isWaiting = false;
            promptContainer.style.display = 'none';
            sendBtn.innerText = "TRANSMIT PACKET";
        }

        // Update history if changed
        const historyHash = JSON.stringify(data.history);
        if (historyHash !== lastHistoryHash) {
            lastHistoryHash = historyHash;
            renderHistory(data.history);
        }
    } catch (e) {
        console.error("Status update failed", e);
    }
}

function renderHistory(history) {
    const atBottom = historyContainer.scrollHeight - historyContainer.scrollTop <= historyContainer.clientHeight + 80;
    
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
            (m.reasoning ? '<div class="reasoning">THOUGHT: ' + escapeHtml(m.reasoning) + '</div>' : '');
        historyContainer.appendChild(div);
    });

    if (atBottom) {
        historyContainer.scrollTo({
            top: historyContainer.scrollHeight,
            behavior: 'smooth'
        });
    }
}

function escapeHtml(text) {
    if (!text) return "";
    const div = document.createElement('div');
    div.innerText = text;
    return div.innerHTML;
}

sendBtn.onclick = async () => {
    const content = messageInput.value.trim();
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
            showToast("SIGNAL TRANSMITTED");
            updateStatus();
        } else {
            showToast("TRANSMISSION FAILED");
        }
    } catch (e) {
        showToast("LINK FAILURE");
    } finally {
        sendBtn.disabled = false;
    }
};

function showToast(msg) {
    toast.innerText = msg;
    toast.style.transform = "translateX(-50%) translateY(0)";
    setTimeout(() => {
        toast.style.transform = "translateX(-50%) translateY(100px)";
    }, 3000);
}

// Polling
updateStatus();
setInterval(updateStatus, 2000);

// Keydown listener for convenient sending
messageInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        sendBtn.click();
    }
});
