package core

import (
	"context"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestAgentSessionExtensionCanProvideCompactionAndReceivesSessionCompact(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	settings.Global.Compaction.KeepRecentTokens = 1
	settings.Global.Compaction.ReserveTokens = 64
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	session := InMemorySession(cwd)
	for i := 0; i < 3; i++ {
		appendSessionMessage(t, session, ai.NewUserMessage(strings.Repeat("u", 40), nil))
		appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks(strings.Repeat("a", 40)), ai.Usage{}, "stop"))
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	var captured *coreext.SessionCompactEvent
	runtime, err := coreext.NewRunner(func(api *coreext.API) error {
		api.On("session_before_compact", func(payload any) {
			event := payload.(*coreext.SessionBeforeCompactEvent)
			event.Result = &coreext.CompactionResult{
				Summary:          "extension summary",
				FirstKeptEntryID: session.Entries[len(session.Entries)-1].ID,
				TokensBefore:     321,
				Details:          map[string]any{"modifiedFiles": []string{"main.go"}},
			}
		})
		api.On("session_compact", func(payload any) {
			captured = payload.(*coreext.SessionCompactEvent)
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.extensionRuntime = runtime

	result, err := agent.Compact("keep short", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result["summary"] != "extension summary" {
		t.Fatalf("result=%#v", result)
	}
	last := session.Entries[len(session.Entries)-1]
	if last.Type != "compaction" || last.Summary != "extension summary" {
		t.Fatalf("last entry=%#v", last)
	}
	if captured == nil || !captured.FromExtension {
		t.Fatalf("session_compact=%#v", captured)
	}
	entry, ok := captured.CompactionEntry.(SessionEntry)
	if !ok || entry.Summary != "extension summary" {
		t.Fatalf("captured compaction entry=%#v", captured.CompactionEntry)
	}
}

func TestAgentSessionExtensionHooksControlTreeNavigation(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	session := InMemorySession(cwd)
	rootID := appendSessionMessage(t, session, ai.NewUserMessage("root", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("reply"), ai.Usage{}, "stop"))
	appendSessionMessage(t, session, ai.NewUserMessage("branch work", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("branch reply"), ai.Usage{}, "stop"))
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	var captured *coreext.SessionTreeEvent
	runtime, err := coreext.NewRunner(func(api *coreext.API) error {
		api.On("session_before_tree", func(payload any) {
			event := payload.(*coreext.SessionBeforeTreeEvent)
			label := "extension label"
			event.Label = &label
			event.Summary = &coreext.BranchSummary{Summary: "extension branch summary", Details: map[string]any{"readFiles": []string{"README.md"}}}
		})
		api.On("session_tree", func(payload any) {
			captured = payload.(*coreext.SessionTreeEvent)
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.extensionRuntime = runtime

	result, err := agent.NavigateTree(context.Background(), rootID, NavigateTreeOptions{Summarize: true, Label: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SummaryEntry == nil || result.SummaryEntry.Summary != "extension branch summary" {
		t.Fatalf("navigate result=%#v", result)
	}
	if captured == nil || !captured.FromExtension {
		t.Fatalf("session_tree=%#v", captured)
	}
	if session.Entries[len(session.Entries)-1].Type != "branch_summary" {
		t.Fatalf("entries=%#v", session.Entries)
	}
	labelFound := false
	for _, entry := range session.Entries {
		if entry.Type == "label" && entry.Label == "extension label" {
			labelFound = true
		}
	}
	if !labelFound {
		t.Fatalf("entries=%#v", session.Entries)
	}
}

func TestAgentSessionExtensionCanCancelTreeNavigationBeforeLeafChange(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	session := InMemorySession(cwd)
	rootID := appendSessionMessage(t, session, ai.NewUserMessage("root", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("reply"), ai.Usage{}, "stop"))
	leafID := appendSessionMessage(t, session, ai.NewUserMessage("branch work", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("branch reply"), ai.Usage{}, "stop"))
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	// NewAgentSession seeds an initial thinking_level_change entry (TS sdk.ts:361-373)
	// for a session without one, which moves the leaf; position the test's target
	// leaf afterward so the cancellation assertion is meaningful.
	if err := session.SetLeaf(leafID); err != nil {
		t.Fatal(err)
	}
	runtime, err := coreext.NewRunner(func(api *coreext.API) error {
		api.On("session_before_tree", func(payload any) {
			payload.(*coreext.SessionBeforeTreeEvent).Cancel = true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.extensionRuntime = runtime

	result, err := agent.NavigateTree(context.Background(), rootID, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cancelled {
		t.Fatalf("result=%#v", result)
	}
	if session.CurrentID == nil || *session.CurrentID != leafID {
		t.Fatalf("leaf changed despite cancellation: %v", session.CurrentID)
	}
}

func TestAgentSessionReloadEmitsExtensionLifecycle(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	var events []string
	runtime, err := coreext.NewRunner(func(api *coreext.API) error {
		api.On("session_shutdown", func(payload any) {
			if event, ok := payload.(*coreext.SessionShutdownEvent); ok {
				events = append(events, "shutdown:"+string(event.Reason))
			}
		})
		api.On("session_start", func(payload any) {
			if event, ok := payload.(*coreext.SessionStartEvent); ok {
				events = append(events, "start:"+string(event.Reason))
			}
		})
		api.OnShutdown(func(context.Context) error {
			events = append(events, "runtime_shutdown")
			return nil
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.extensionRuntime = runtime
	originalSettings := agent.Settings

	if err := agent.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The outgoing runtime receives shutdown:reload and then has its runtime
	// shutdown invoked. session_start(reload) is emitted onto the REBUILT runtime,
	// which has no test handlers, so it does not appear in this listener's log.
	// This is the post-fix behavior: the runtime is respawned, not left dead.
	want := []string{"shutdown:reload", "runtime_shutdown"}
	if len(events) != len(want) {
		t.Fatalf("events=%#v want=%#v", events, want)
	}
	for i, value := range want {
		if events[i] != value {
			t.Fatalf("events=%#v want=%#v", events, want)
		}
	}
	// P1-15: Reload must REBUILD the extension runtime (not leave a.extensionRuntime
	// pointing at the dead/shut-down runner).
	if agent.extensionRuntime == nil {
		t.Fatal("extension runtime was not rebuilt after reload (nil)")
	}
	if agent.extensionRuntime == runtime {
		t.Fatal("extension runtime was not respawned after reload (still the old, shut-down runner)")
	}
	// P2-30: Reload must re-read settings from disk (fresh SettingsManager).
	if agent.Settings == originalSettings {
		t.Fatal("Reload did not re-read settings from disk (SettingsManager not replaced)")
	}
}

// TestAgentSessionReloadRebuildsFunctionalRuntime verifies the P1-15 fix end to
// end: after reload, the rebuilt extension runtime exposes the extension's tools
// again (extensions are not dead) and preserves the previous flag values.
func TestAgentSessionReloadRebuildsFunctionalRuntime(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}

	factory := func(api *coreext.API) error {
		api.RegisterFlag(coreext.FlagDefinition{Name: "myflag", Type: "string", Default: "default"})
		api.RegisterTool(coreext.ToolDefinition{
			Name:        "ext_tool",
			Description: "extension tool",
			Parameters:  map[string]any{"type": "object"},
			Execute: func(context.Context, []byte) (ai.ToolResult, error) {
				return ai.ToolResult{Content: ai.TextBlocks("ok")}, nil
			},
		})
		return nil
	}

	// Build the initial runtime with the flag overridden, mirroring how the host
	// seeds CLI/extension flag values at construction.
	runtime, _ := loadExtensionRuntime(context.Background(), nil, []coreext.Factory{factory}, map[string]any{"myflag": "overridden"})
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	agent.extensionRuntime = runtime
	// Wire the factory so Reload's _buildRuntime equivalent rebuilds from it.
	agent.ResourceLoaderOptions.ExtensionFactories = []coreext.Factory{factory}

	if _, ok := runtime.ToolDefinition("ext_tool"); !ok {
		t.Fatal("setup: extension tool missing before reload")
	}
	if got := runtime.FlagValue("myflag"); got != "overridden" {
		t.Fatalf("setup: flag value before reload = %#v", got)
	}

	if err := agent.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	rebuilt := agent.extensionRuntime
	if rebuilt == nil || rebuilt == runtime {
		t.Fatalf("runtime not respawned: rebuilt=%p old=%p", rebuilt, runtime)
	}
	// Extensions are alive again: the tool is registered on the new runtime.
	if _, ok := rebuilt.ToolDefinition("ext_tool"); !ok {
		t.Fatal("extension tool missing after reload (extensions died)")
	}
	// Flag values are preserved across the rebuild.
	if got := rebuilt.FlagValue("myflag"); got != "overridden" {
		t.Fatalf("flag value not preserved after reload = %#v", got)
	}
}
