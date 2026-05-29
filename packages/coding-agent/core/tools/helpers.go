package tools

import (
	"github.com/guanshan/pi-go/packages/ai"
)

func toolError(text string) ai.ToolResult {
	return ai.ToolResult{Content: ai.TextBlocks(text), IsError: true}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
