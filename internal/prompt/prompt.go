package prompt

import (
	"fmt"
	"strings"
	"time"

	"X01/internal/db"
)

func RenderPrompt(stateText string, memories []db.Memory, balance float64) string {
	now := time.Now().Format("2006-01-02 15:04:05")

	memoryBlocks := "No memories stored."
	if len(memories) > 0 {
		var lines []string
		for _, m := range memories {
			lines = append(lines, fmt.Sprintf("[%d] %s", m.ID, m.Content))
		}
		memoryBlocks = strings.Join(lines, "\n")
	}

	template := `You are an friendly AI, an autonomous AI agent running in the framework X01

You are designed for the continuous operation.

HOW THE FRAMEWORK WORKS:
- In a while loop, your response is triggered by an automated heartbeat pulse.
- First user message is dynamically updated with the current state of the world. 

CURRENT CONTEXT:
- Date: %s
- Balance: $%.2f
- Environment: Python Docker image (Go Core Port)
- Persistence: Docker Sandbox is mapped to your local './sandbox' folder. All files you create there are persistent.

CORE INSTRUCTIONS:
- Use tools to interact with your environment.
- If you are unsure or need human guidance, use the ` + "`ask_user`" + ` tool to pause and wait for a reply.
- Your 'PRIME CONTEXT' is your long-term configuration and mission state. Use ` + "`update_state`" + ` to evolve your logic.
- Your 'DISCRETE MEMORIES' are specific facts you've recorded. Use ` + "`memorize`" + ` and ` + "`forget_memory`" + ` for these.

COMMUNICATION PROTOCOL:
- You will receive periodic ` + "`user`" + ` messages labeled "HEARTBEAT PULSE". These are automated triggers. 
- Do NOT try to reply to heartbeats as if they are a human. They only exist to give you execution turns.
- Only use ` + "`ask_user`" + ` when you actually need to speak to the user.
- When calling ` + "`ask_user`" + `, the terminal will block and wait for a direct response from the human.

DISCRETE MEMORIES (FACTS):
%s
%s
PRIME CONTEXT (CORE LOGIC):
%s
`
	pendingCount, _ := db.GetPendingCount()
	notifications := ""
	if pendingCount > 0 {
		notifications = fmt.Sprintf("\n[NOTIFICATIONS]\n- You have %d pending messages from the human. Use the 'check_messages' tool when you are ready to read and respond to them.\n", pendingCount)
	}

	return fmt.Sprintf(template, now, balance, memoryBlocks, notifications, stateText)
}
