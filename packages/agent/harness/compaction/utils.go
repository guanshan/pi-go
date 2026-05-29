package compaction

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

type FileOperations struct {
	Read    map[string]struct{} `json:"read"`
	Written map[string]struct{} `json:"written"`
	Edited  map[string]struct{} `json:"edited"`
}

func CreateFileOps() FileOperations {
	return FileOperations{
		Read:    map[string]struct{}{},
		Written: map[string]struct{}{},
		Edited:  map[string]struct{}{},
	}
}

func ExtractFileOpsFromMessage(message agent.AgentMessage, fileOps *FileOperations) {
	if fileOps == nil {
		return
	}
	if _, ok := ai.AsAssistantMessage(message); !ok {
		return
	}
	ensureFileOps(fileOps)
	for _, block := range ai.MessageBlocks(message) {
		if block.Type != "toolCall" || block.Name == "" || len(block.Arguments) == 0 {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal(block.Arguments, &args); err != nil {
			continue
		}
		path, _ := args["path"].(string)
		if path == "" {
			continue
		}
		switch block.Name {
		case "read":
			fileOps.Read[path] = struct{}{}
		case "write":
			fileOps.Written[path] = struct{}{}
		case "edit":
			fileOps.Edited[path] = struct{}{}
		}
	}
}

func ComputeFileLists(fileOps FileOperations) (readFiles, modifiedFiles []string) {
	modified := map[string]struct{}{}
	for file := range fileOps.Edited {
		modified[file] = struct{}{}
	}
	for file := range fileOps.Written {
		modified[file] = struct{}{}
	}
	for file := range fileOps.Read {
		if _, ok := modified[file]; !ok {
			readFiles = append(readFiles, file)
		}
	}
	for file := range modified {
		modifiedFiles = append(modifiedFiles, file)
	}
	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)
	return readFiles, modifiedFiles
}

func FormatFileOperations(readFiles, modifiedFiles []string) string {
	var sections []string
	if len(readFiles) > 0 {
		sections = append(sections, "<read-files>\n"+strings.Join(readFiles, "\n")+"\n</read-files>")
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, "<modified-files>\n"+strings.Join(modifiedFiles, "\n")+"\n</modified-files>")
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

func SerializeConversation(messages []ai.Message) string {
	parts := []string{}
	for _, message := range messages {
		switch message.(type) {
		case ai.UserMessage, *ai.UserMessage:
			if text := ai.MessageText(message); text != "" {
				parts = append(parts, "[User]: "+text)
			}
		case ai.AssistantMessage, *ai.AssistantMessage:
			var textParts, thinkingParts, toolCalls []string
			for _, block := range ai.MessageBlocks(message) {
				switch block.Type {
				case "text":
					textParts = append(textParts, block.Text)
				case "thinking":
					thinkingParts = append(thinkingParts, block.Thinking)
				case "toolCall":
					toolCalls = append(toolCalls, block.Name+"("+formatToolCallArgs(block.Arguments)+")")
				}
			}
			if len(thinkingParts) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinkingParts, "\n"))
			}
			if len(textParts) > 0 {
				parts = append(parts, "[Assistant]: "+strings.Join(textParts, "\n"))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
			}
		case ai.ToolResultMessage, *ai.ToolResultMessage:
			if text := ai.MessageText(message); text != "" {
				parts = append(parts, "[Tool result]: "+truncateForSummary(text, 2000))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func formatToolCallArgs(raw json.RawMessage) string {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return string(raw)
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		encoded, err := json.Marshal(args[key])
		if err != nil {
			encoded = []byte("[unserializable]")
		}
		parts = append(parts, key+"="+string(encoded))
	}
	return strings.Join(parts, ", ")
}

func truncateForSummary(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	return fmt.Sprintf("%s\n\n[... %d more characters truncated]", text[:maxChars], len(text)-maxChars)
}

func ensureFileOps(fileOps *FileOperations) {
	if fileOps.Read == nil {
		fileOps.Read = map[string]struct{}{}
	}
	if fileOps.Written == nil {
		fileOps.Written = map[string]struct{}{}
	}
	if fileOps.Edited == nil {
		fileOps.Edited = map[string]struct{}{}
	}
}
