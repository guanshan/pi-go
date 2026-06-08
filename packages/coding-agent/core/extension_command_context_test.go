package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestExtensionCommandContextWaitForIdleReturnsOnIdle(t *testing.T) {
	agent := &AgentSession{Session: InMemorySession(t.TempDir())}
	raw, err := agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{Name: "waitForIdle"})
	if err != nil {
		t.Fatalf("waitForIdle: %v", err)
	}
	if string(raw) != "null" {
		t.Fatalf("waitForIdle result = %s, want null", raw)
	}
}

func TestExtensionCommandContextNavigateTreeRequiresTarget(t *testing.T) {
	agent := &AgentSession{Session: InMemorySession(t.TempDir())}
	if _, err := agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{
		Name:   "navigateTree",
		Params: json.RawMessage(`{"targetId":""}`),
	}); err == nil {
		t.Fatal("navigateTree with empty target should error")
	}
}

func TestExtensionCommandContextUnsupportedActionsRejectClearly(t *testing.T) {
	agent := &AgentSession{Session: InMemorySession(t.TempDir())}
	for _, name := range []string{"newSession", "fork", "switchSession", "getSystemPromptOptions"} {
		_, err := agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{Name: name})
		if err == nil {
			t.Fatalf("%s should reject as unsupported", name)
		}
		if !strings.Contains(err.Error(), "not supported by this host") {
			t.Fatalf("%s error = %q, want 'not supported by this host'", name, err)
		}
	}
}
