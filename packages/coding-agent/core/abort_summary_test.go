package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

type blockingSummaryProvider struct {
	api     string
	started chan struct{}
	release chan struct{}
}

func (p *blockingSummaryProvider) API() string { return p.api }

func (p *blockingSummaryProvider) Stream(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.stream(ctx, req)
}

func (p *blockingSummaryProvider) StreamSimple(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.stream(ctx, req)
}

func (p *blockingSummaryProvider) stream(ctx context.Context, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(2)
	go func() {
		select {
		case p.started <- struct{}{}:
		default:
		}
		select {
		case <-p.release:
			msg := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("summary complete"), ai.Usage{}, "stop")
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: msg.StopReason, Message: msg})
		case <-ctx.Done():
			aborted := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "aborted")
			aborted.ErrorMessage = ctx.Err().Error()
			stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: aborted})
		}
	}()
	return stream
}

func TestAgentSessionAbortCompactionCancelsInFlightSummary(t *testing.T) {
	provider := &blockingSummaryProvider{api: "abort-compaction-api", started: make(chan struct{}, 1), release: make(chan struct{})}
	ai.RegisterProvider(provider, "abort-compaction-api")
	defer ai.UnregisterProviders("abort-compaction-api")

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	settings.Global.Compaction.KeepRecentTokens = 1
	settings.Global.Compaction.ReserveTokens = 64
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model := ai.Model{Provider: "unit", ID: "abort-compaction-model", API: provider.api, MaxOutput: 2048}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}

	appendSessionMessage(t, session, ai.NewUserMessage("inspect old code", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("done"), ai.Usage{}, "stop"))
	appendSessionMessage(t, session, ai.NewUserMessage("patch it", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("done again"), ai.Usage{}, "stop"))
	appendSessionMessage(t, session, ai.NewUserMessage("verify latest state", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("latest response"), ai.Usage{}, "stop"))

	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	done := make(chan error, 1)
	go func() {
		_, err := agent.Compact("abort me", nil)
		done <- err
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for compaction summary to start")
	}
	agent.AbortCompaction()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Fatalf("compact error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("compaction did not stop after abort")
	}
	if last := session.Entries[len(session.Entries)-1]; last.Type == "compaction" {
		t.Fatalf("unexpected compaction entry=%#v", last)
	}
}

func TestAgentSessionAbortBranchSummaryCancelsNavigateTree(t *testing.T) {
	provider := &blockingSummaryProvider{api: "abort-branch-summary-api", started: make(chan struct{}, 1), release: make(chan struct{})}
	ai.RegisterProvider(provider, "abort-branch-summary-api")
	defer ai.UnregisterProviders("abort-branch-summary-api")

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model := ai.Model{Provider: "unit", ID: "abort-branch-model", API: provider.api, ContextWindow: 4096, MaxOutput: 2048}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}

	rootID := appendSessionMessage(t, session, ai.NewUserMessage("root", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("first reply"), ai.Usage{}, "stop"))
	appendSessionMessage(t, session, ai.NewUserMessage("branch work", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("branch reply"), ai.Usage{}, "stop"))

	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	done := make(chan struct {
		result NavigateTreeResult
		err    error
	}, 1)
	go func() {
		result, err := agent.NavigateTree(context.Background(), rootID, NavigateTreeOptions{Summarize: true, CustomInstructions: "keep short"})
		done <- struct {
			result NavigateTreeResult
			err    error
		}{result: result, err: err}
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for branch summary to start")
	}
	agent.AbortBranchSummary()

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("navigate error=%v", outcome.err)
		}
		if !outcome.result.Cancelled {
			t.Fatalf("navigate result=%#v", outcome.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("navigate tree did not stop after branch summary abort")
	}
	for _, entry := range session.Entries {
		if entry.Type == "branch_summary" {
			t.Fatalf("unexpected branch summary entry=%#v", entry)
		}
	}
}
