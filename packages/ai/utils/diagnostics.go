package utils

import (
	"fmt"
	"reflect"
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
		// Mirror TS extractDiagnosticError: preserve the concrete error type
		// name (analog of JS error.name), populate code when the error exposes
		// one, and omit the stack when none is available (Go errors carry no
		// stack, so we do not synthesize one).
		return DiagnosticErrorInfo{
			Name:    diagnosticErrorName(err),
			Message: err.Error(),
			Code:    diagnosticErrorCode(err),
		}
	}
	return DiagnosticErrorInfo{Name: "ThrownValue", Message: FormatThrownValue(value)}
}

// diagnosticErrorName returns the concrete error type name (best-effort analog
// of JS error.name), dereferencing pointers. Falls back to "Error" when the
// type carries no useful name (e.g. anonymous types).
func diagnosticErrorName(err error) string {
	t := reflect.TypeOf(err)
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t != nil {
		if name := t.Name(); name != "" {
			return name
		}
	}
	return "Error"
}

// diagnosticErrorCode returns a string|number code when the error exposes one,
// mirroring TS reading (error as { code?: unknown }).code. Returns nil when no
// usable code is present.
func diagnosticErrorCode(err error) any {
	switch c := err.(type) {
	case interface{ Code() string }:
		return c.Code()
	case interface{ Code() int }:
		return c.Code()
	case interface{ Code() int64 }:
		return c.Code()
	case interface{ Code() float64 }:
		return c.Code()
	}
	return nil
}

func CreateAssistantMessageDiagnostic(kind string, err any, details map[string]any) AssistantMessageDiagnostic {
	info := ExtractDiagnosticError(err)
	return AssistantMessageDiagnostic{Type: kind, Timestamp: time.Now().UnixMilli(), Error: &info, Details: details}
}
