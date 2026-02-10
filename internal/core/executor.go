package core

import (
	"context"
	"fmt"
	"time"
)

type ToolFunc func(ctx context.Context, args map[string]interface{}) string

func ExecuteTool(ctx context.Context, fn ToolFunc, args map[string]interface{}, timeout time.Duration) string {
	resChan := make(chan string, 1)

	// Create a sub-context for the tool timeout
	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				resChan <- fmt.Sprintf("CRITICAL ERROR: Tool panicked: %v", r)
			}
		}()
		resChan <- fn(toolCtx, args)
	}()

	select {
	case res := <-resChan:
		// Truncate output if too long
		if len(res) > 10000 {
			res = res[:5000] + "\n...[TRUNCATED]...\n" + res[len(res)-5000:]
		}
		return res
	case <-toolCtx.Done():
		if toolCtx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("ERROR: Tool timed out after %v", timeout)
		}
		return "ERROR: Tool execution cancelled"
	}
}
