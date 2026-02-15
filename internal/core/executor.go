package core

import (
	"context"
	"fmt"
	"strings"
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
			head := res[:5000]
			tail := res[len(res)-5000:]
			middle := res[5000 : len(res)-5000]

			charCount := len(middle)
			lineCount := strings.Count(middle, "\n")

			res = fmt.Sprintf("%s\n...[TRUNCATED %d characters and %d lines]...\n%s", head, charCount, lineCount, tail)
		}
		return res
	case <-toolCtx.Done():
		if toolCtx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("ERROR: Tool timed out after %v", timeout)
		}
		return "ERROR: Tool execution cancelled"
	}
}
