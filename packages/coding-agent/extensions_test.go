package codingagent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestExtensionRunnerShutdownRunsHandlersInReverseOrder(t *testing.T) {
	var calls []string
	var emitted []string
	expected := errors.New("shutdown failed")
	runner, err := NewExtensionRunner(
		func(api *ExtensionAPI) error {
			api.RegisterTool(DefineTool("inline", "Inline tool", map[string]any{"type": "object"}, func(context.Context, []byte) (ai.ToolResult, error) {
				return ai.ToolResult{}, nil
			}))
			api.RegisterCommand("inline", "Inline command")
			api.On("custom_event", func(any) {})
			api.OnShutdown(func(context.Context) error {
				calls = append(calls, "first")
				return nil
			})
			return nil
		},
		func(api *ExtensionAPI) error {
			api.OnShutdown(func(context.Context) error {
				calls = append(calls, "second")
				return expected
			})
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stopListening := runner.OnError(func(err error) {
		emitted = append(emitted, err.Error())
	})
	defer stopListening()

	if !runner.HasHandlers("shutdown") || !runner.HasHandlers("custom_event") {
		t.Fatal("expected runtime to report registered handlers")
	}
	if got := runner.GetRegisteredCommands(); len(got) != 1 || got[0].Name != "inline" {
		t.Fatalf("commands=%#v", got)
	}
	tool, ok := runner.GetToolDefinition("inline")
	if !ok || tool.Name != "inline" {
		t.Fatalf("tool=%#v ok=%v", tool, ok)
	}

	err = runner.Shutdown(context.Background())
	if !errors.Is(err, expected) {
		t.Fatalf("shutdown error = %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"second", "first"}) {
		t.Fatalf("shutdown order = %#v", calls)
	}
	if !strings.Contains(err.Error(), expected.Error()) {
		t.Fatalf("shutdown error missing detail: %v", err)
	}
	if !reflect.DeepEqual(emitted, []string{expected.Error()}) {
		t.Fatalf("emitted errors=%#v", emitted)
	}
}
