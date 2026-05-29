package utils

import (
	"fmt"
	"runtime/debug"
	"time"
)

type DiagnosticErrorInfo struct {
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
	Code    any    `json:"code,omitempty"`
}

type AssistantMessageDiagnostic struct {
	Type      string               `json:"type"`
	Timestamp int64                `json:"timestamp"`
	Error     *DiagnosticErrorInfo `json:"error,omitempty"`
	Details   map[string]any       `json:"details,omitempty"`
}

func FormatThrownValue(value any) string {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	return fmt.Sprint(value)
}

func ExtractDiagnosticError(value any) DiagnosticErrorInfo {
	if err, ok := value.(error); ok {
		return DiagnosticErrorInfo{Name: "Error", Message: err.Error(), Stack: string(debug.Stack())}
	}
	return DiagnosticErrorInfo{Name: "ThrownValue", Message: FormatThrownValue(value)}
}

func CreateAssistantMessageDiagnostic(kind string, err any, details map[string]any) AssistantMessageDiagnostic {
	info := ExtractDiagnosticError(err)
	return AssistantMessageDiagnostic{Type: kind, Timestamp: time.Now().UnixMilli(), Error: &info, Details: details}
}
