package core

import (
	"context"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// captureRequestProvider records the ChatRequest(s) it receives so tests can
// assert what actually reaches the model.
type captureRequestProvider struct {
	reqs *[]ai.ChatRequest
}

func (p *captureRequestProvider) API() string { return "convert-to-llm-capture" }

func (p *captureRequestProvider) Stream(_ context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	*p.reqs = append(*p.reqs, req)
	stream := ai.NewAssistantMessageEventStream(4)
	go func() {
		final := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "")})
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: final})
	}()
	return stream
}

func (p *captureRequestProvider) StreamSimple(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.Stream(ctx, registry, req)
}

func providerMessageContains(reqs []ai.ChatRequest, token string) bool {
	if len(reqs) == 0 {
		return false
	}
	for _, m := range reqs[0].Messages {
		if strings.Contains(ai.MessageText(m), token) {
			return true
		}
	}
	return false
}

// TestConvertSessionMessagesToLLM is a table of the typed session messages that
// BuildContext can emit; each must survive the loop's ConvertToLLM step and
// reach the provider, otherwise the model loses that context (e.g. the
// post-compaction summary). Guards against a regression where the loop falls
// back to defaultConvertToLLM and silently drops them.
func TestConvertSessionMessagesToLLM(t *testing.T) {
	cases := []struct {
		name  string
		entry SessionEntry
		token string
	}{
		{"compaction", SessionEntry{Type: "compaction", ID: "c1", Summary: "COMPACT_TOKEN_ABC"}, "COMPACT_TOKEN_ABC"},
		{"branch_summary", SessionEntry{Type: "branch_summary", ID: "b1", Summary: "BRANCH_TOKEN_ABC"}, "BRANCH_TOKEN_ABC"},
		{"custom_message", SessionEntry{Type: "custom_message", ID: "x1", Content: "CUSTOM_TOKEN_ABC"}, "CUSTOM_TOKEN_ABC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reqs []ai.ChatRequest
			provider := &captureRequestProvider{reqs: &reqs}
			ai.RegisterProvider(provider, provider.API())
			defer ai.UnregisterProviders(provider.API())

			cwd := t.TempDir()
			settings := NewSettingsManager(cwd, t.TempDir())
			registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
			model := ai.Model{Provider: "unit", ID: "convert-model", API: provider.API()}
			session := InMemorySession(cwd)
			if err := session.Append(tc.entry); err != nil {
				t.Fatal(err)
			}
			resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
			agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
			if err := agent.Prompt(context.Background(), "hello", nil, func(ai.Event) {}); err != nil {
				t.Fatal(err)
			}
			if !providerMessageContains(reqs, tc.token) {
				t.Fatalf("%s summary/content %q did not reach the provider; messages=%v", tc.name, tc.token, reqs)
			}
		})
	}
}

// TestConvertSessionMessagesToLLMUnit exercises the converter directly, including
// the bash-excluded-from-context drop and the standard pass-through paths.
func TestConvertSessionMessagesToLLMUnit(t *testing.T) {
	exclude := BashExecutionMessage{Role: "bashExecution", Command: "ls", Output: "x", ExcludeFromContext: true}
	include := BashExecutionMessage{Role: "bashExecution", Command: "pwd", Output: "/tmp"}
	user := ai.NewUserMessage("hi", nil)
	in := []ai.Message{user, CompactionSummaryMessage{Summary: "S"}, exclude, include, CustomSessionMessage{Content: nil}, CustomSessionMessage{Content: true}}

	out, err := convertSessionMessagesToLLM(in)
	if err != nil {
		t.Fatal(err)
	}
	// user + compaction(user) + include(user) + scalar custom(user);
	// exclude and empty custom are dropped.
	if len(out) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(out), out)
	}
	for _, m := range out {
		if ai.MessageRole(m) != "user" {
			t.Fatalf("expected all converted messages to be user role, got %q", ai.MessageRole(m))
		}
	}
	if !strings.Contains(ai.MessageText(out[1]), "S") {
		t.Fatalf("compaction summary text missing: %q", ai.MessageText(out[1]))
	}
	if !strings.Contains(ai.MessageText(out[2]), "pwd") {
		t.Fatalf("included bash output missing: %q", ai.MessageText(out[2]))
	}
	if ai.MessageText(out[3]) != "true" {
		t.Fatalf("scalar custom text missing: %q", ai.MessageText(out[3]))
	}
}
