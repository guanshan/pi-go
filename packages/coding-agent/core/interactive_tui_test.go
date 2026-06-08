package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestSlashCommandSuggestions(t *testing.T) {
	suggestions := slashCommandSuggestions("/mo")
	if !slices.Contains(suggestions, "/model") {
		t.Fatalf("expected /model suggestion, got %#v", suggestions)
	}
	if got := slashCommandSuggestions("/model gpt"); len(got) != 0 {
		t.Fatalf("expected no suggestions after arguments, got %#v", got)
	}
}

func TestInteractiveBusySubmitQueuesInsteadOfDroppingInput(t *testing.T) {
	for _, tc := range []struct {
		name     string
		text     string
		behavior StreamingBehavior
		followUp bool
	}{
		{name: "enter steers", text: "please adjust"},
		{name: "alt enter follows up", text: "next turn", behavior: StreamingFollowUp, followUp: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := testInteractiveRuntime(t)
			model, err := newInteractiveModel(context.Background(), runtime, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			model.busy = true
			model.busyKind = interactiveBusyAgent
			model.input.SetValue(tc.text)
			cmd := model.submitInputWithBehavior(tc.behavior)
			if cmd == nil || cmd() != (interactiveQueueDoneMsg{}) {
				t.Fatalf("queue cmd failed: %#v", cmd)
			}
			if got := strings.TrimSpace(model.input.Value()); got != "" {
				t.Fatalf("input not cleared: %q", got)
			}
			agent := runtime.Session()
			if tc.followUp && (len(agent.followUpQueue) != 1 || agent.followUpQueue[0].Message != tc.text) {
				t.Fatalf("followUpQueue=%#v", agent.followUpQueue)
			}
			if !tc.followUp && (len(agent.steeringQueue) != 1 || agent.steeringQueue[0].Message != tc.text) {
				t.Fatalf("steeringQueue=%#v", agent.steeringQueue)
			}
		})
	}
}

func TestInteractiveCtrlCClearsThenQuits(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("draft")
	if cmd := model.handleCtrlC(); cmd != nil {
		t.Fatalf("first ctrl+c returned cmd %#v", cmd)
	}
	if got := strings.TrimSpace(model.input.Value()); got != "" {
		t.Fatalf("input not cleared: %q", got)
	}
	cmd := model.handleCtrlC()
	if cmd == nil {
		t.Fatal("second ctrl+c should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("second ctrl+c did not quit")
	}
}

func TestInteractiveEscapeCancelsRunningCommand(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.busy = true
	model.busyKind = interactiveBusyCommand
	cmdCtx := model.beginCommand()
	if model.commandCancel == nil {
		t.Fatal("beginCommand did not record a cancel func")
	}
	if cmd := model.handleEscape(); cmd != nil {
		t.Fatalf("escape on a busy command should not return a cmd, got %#v", cmd)
	}
	select {
	case <-cmdCtx.Done():
	default:
		t.Fatal("escape did not cancel the running command context")
	}
	if model.commandCancel != nil {
		t.Fatal("escape did not clear commandCancel")
	}
}

func TestInteractiveBashSubmitIsCancellable(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("!sleep 30")
	cmd := model.submitInputWithBehavior(StreamingSteer)
	if cmd == nil {
		t.Fatal("bash submit returned nil cmd")
	}
	if model.busyKind != interactiveBusyCommand {
		t.Fatalf("busyKind = %q, want command", model.busyKind)
	}
	if model.commandCancel == nil {
		t.Fatal("bash submit did not set a per-command cancel func")
	}
	// Escape must cancel the command without executing the queued cmd closure.
	if escCmd := model.handleEscape(); escCmd != nil {
		t.Fatalf("escape returned cmd %#v", escCmd)
	}
	if model.commandCancel != nil {
		t.Fatal("escape did not clear commandCancel after cancelling bash command")
	}
}

func TestInteractiveLargePasteFoldsAndExpandsOnSubmit(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := numberedLines(12)
	model.Update(tea.PasteMsg{Content: lines})
	marker := "[paste #1 +12 lines]"
	if got := model.input.Value(); got != marker {
		t.Fatalf("input after large paste=%q want %q", got, marker)
	}
	if expanded := model.expandedInputText(); expanded != lines {
		t.Fatalf("expanded paste=%q want original", expanded)
	}

	cmd := model.submitInputWithBehavior("")
	if cmd == nil {
		t.Fatal("submit returned nil command")
	}
	msg := cmd()
	if done, ok := msg.(interactivePromptDoneMsg); !ok || done.Err != nil {
		t.Fatalf("prompt cmd result=%#v", msg)
	}
	if model.input.Value() != "" {
		t.Fatalf("input not reset: %q", model.input.Value())
	}
	if len(model.pastes) != 0 {
		t.Fatalf("pastes not cleared: %#v", model.pastes)
	}
	if len(model.history) != 1 || model.history[0] != lines {
		t.Fatalf("history=%#v want expanded paste", model.history)
	}
	ctx := runtime.Session().Session.BuildContext()
	if len(ctx.Messages) < 1 || ai.MessageText(ctx.Messages[0]) != lines {
		t.Fatalf("session messages=%#v want expanded paste", ctx.Messages)
	}
	rendered := model.renderTranscript(100)
	if !strings.Contains(rendered, marker) {
		t.Fatalf("transcript should echo collapsed marker, got:\n%s", rendered)
	}
}

func TestInteractivePasteCleaningAndPathSpacing(t *testing.T) {
	if got := cleanPastedText("a\r\nb\x1b[106;5uc\t"); got != "a\nb\nc    " {
		t.Fatalf("cleanPastedText=%q", got)
	}
	if !shouldPrependSpaceForPastedPath("./file.go", 'x') {
		t.Fatal("expected path paste after word rune to request a leading space")
	}
	if shouldPrependSpaceForPastedPath("./file.go", ' ') {
		t.Fatal("space before path should not request another leading space")
	}
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("open")
	model.input.MoveToEnd()
	model.Update(tea.PasteMsg{Content: "./file.go"})
	if got := model.input.Value(); got != "open ./file.go" {
		t.Fatalf("path paste input=%q", got)
	}
}

func TestInteractiveLargeCharPasteUsesCharMarker(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("x", largePasteCharThreshold+1)
	model.handlePaste(text)
	if got := model.input.Value(); got != "[paste #1 1001 chars]" {
		t.Fatalf("char paste marker=%q", got)
	}
	if model.expandedInputText() != text {
		t.Fatal("char paste marker did not expand to original text")
	}
}

func TestInteractiveToolTranscriptPreviewAndExpand(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.messages = nil
	model.appendMessageEntry(interactiveMessage{
		Role:     interactiveRoleTool,
		Kind:     interactiveMessageTool,
		ToolName: "read",
		Text:     numberedLines(25),
	})

	collapsed := model.renderTranscript(80)
	if !strings.Contains(collapsed, "[read]") || !strings.Contains(collapsed, "... 5 more lines") {
		t.Fatalf("collapsed transcript missing title/preview notice:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "line-01") || !strings.Contains(collapsed, "line-25") {
		t.Fatalf("collapsed transcript did not show tail preview:\n%s", collapsed)
	}

	model.toolsExpanded = true
	expanded := model.renderTranscript(80)
	if !strings.Contains(expanded, "line-01") || !strings.Contains(expanded, "ctrl+o to collapse") {
		t.Fatalf("expanded transcript missing full output/collapse hint:\n%s", expanded)
	}
}

func TestInteractiveToolEventRendersArgsAndErrorState(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.messages = nil
	model.toolsExpanded = true

	model.applyAgentEvent(ai.Event{
		"type":       "tool_execution_start",
		"toolName":   "edit",
		"toolCallId": "call-1",
		"args":       json.RawMessage(`{"file":"main.go"}`),
	})
	model.applyAgentEvent(ai.Event{
		"type":       "tool_execution_end",
		"toolName":   "edit",
		"toolCallId": "call-1",
		"result":     ai.ToolResult{Content: ai.TextBlocks("-old\n+new"), IsError: true},
		"isError":    true,
	})

	rendered := model.renderTranscript(80)
	for _, want := range []string{"[edit]", "error", "args:", "\"file\": \"main.go\"", "-old", "+new"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered transcript missing %q:\n%s", want, rendered)
		}
	}
}

func TestInteractiveBashTranscriptPreview(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.messages = nil
	exit := 1
	model.appendBashMessage(interactiveBashResult{
		Command:  "printf lines",
		Output:   numberedLines(25),
		ExitCode: &exit,
		IsError:  true,
	})

	collapsed := model.renderTranscript(80)
	if !strings.Contains(collapsed, "$ printf lines") || !strings.Contains(collapsed, "... 5 more lines") || !strings.Contains(collapsed, "exit 1") {
		t.Fatalf("collapsed bash transcript missing command/status:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "line-01") {
		t.Fatalf("collapsed bash transcript showed hidden head:\n%s", collapsed)
	}
	model.toolsExpanded = true
	expanded := model.renderTranscript(80)
	if !strings.Contains(expanded, "line-01") || !strings.Contains(expanded, "ctrl+o to collapse") {
		t.Fatalf("expanded bash transcript missing full output/collapse hint:\n%s", expanded)
	}
}

func TestInteractiveModelAndResourceSuggestions(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	agent := runtime.Session()
	agent.Resources.PromptTemplates["review"] = PromptTemplate{Name: "review", Content: "review prompt"}
	agent.Resources.Skills["go"] = Skill{Name: "go", Content: "skill"}
	agent.SetScopedModels([]ScopedModel{{Model: ai.Model{Provider: "openai", ID: "zz-test"}}})

	suggestions := interactiveSuggestions("/re", agent)
	if !slices.Contains(suggestions, "/review") {
		t.Fatalf("prompt template suggestion missing: %#v", suggestions)
	}
	suggestions = interactiveSuggestions("/skill:g", agent)
	if !slices.Contains(suggestions, "/skill:go") {
		t.Fatalf("skill suggestion missing: %#v", suggestions)
	}
	suggestions = interactiveSuggestions("/model zz", agent)
	if !slices.Contains(suggestions, "/model openai/zz-test") {
		t.Fatalf("model suggestion missing: %#v", suggestions)
	}
}

func TestInteractiveAutocompleteDropdownRendersDescriptions(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	agent := runtime.Session()
	agent.Resources.PromptTemplates["review"] = PromptTemplate{Name: "review", Content: "Review code carefully\nwith extra context"}
	agent.Resources.Skills["go"] = Skill{Name: "go", Description: "Use Go project guidance"}
	agent.SetScopedModels([]ScopedModel{{Model: ai.Model{Provider: "openai", ID: "zz-test"}}})
	api := coreext.NewAPI()
	api.RegisterCommandHandler("deploy", "Deploy the current app", nil)
	agent.extensionRuntime = coreext.NewRunnerWithAPI(api)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.width = 100

	model.input.SetValue("/mo")
	rendered := model.renderSuggestions()
	if !strings.Contains(rendered, "/model") || !strings.Contains(rendered, "Select model") {
		t.Fatalf("slash command description missing:\n%s", rendered)
	}
	for _, suggestion := range model.currentSuggestions() {
		if strings.Contains(suggestion, "Select model") {
			t.Fatalf("currentSuggestions should contain values only, got %#v", model.currentSuggestions())
		}
	}

	model.input.SetValue("/review")
	if rendered = model.renderSuggestions(); !strings.Contains(rendered, "Review code carefully") {
		t.Fatalf("prompt template description missing:\n%s", rendered)
	}
	model.input.SetValue("/skill:g")
	if rendered = model.renderSuggestions(); !strings.Contains(rendered, "Use Go project guidance") {
		t.Fatalf("skill description missing:\n%s", rendered)
	}
	model.input.SetValue("/dep")
	if rendered = model.renderSuggestions(); !strings.Contains(rendered, "Deploy the current app") {
		t.Fatalf("extension command description missing:\n%s", rendered)
	}
	model.input.SetValue("/model zz")
	if rendered = model.renderSuggestions(); !strings.Contains(rendered, "/model openai/zz-test") || !strings.Contains(rendered, "openai") {
		t.Fatalf("model suggestion description missing:\n%s", rendered)
	}
}

func TestInteractiveAutocompletePrefixesSourceDescriptions(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	agent := runtime.Session()
	agent.Resources.PromptTemplates["review"] = PromptTemplate{
		Name:       "review",
		Content:    "Review code carefully",
		SourceInfo: ResourceSourceInfo{Source: "auto", Scope: "project"},
	}
	agent.Resources.Skills["go"] = Skill{
		Name:        "go",
		Description: "Use Go guidance",
		SourceInfo:  ResourceSourceInfo{Source: "local", Scope: "user"},
	}
	agent.Resources.Skills["ship"] = Skill{
		Name:        "ship",
		Description: "Ship package flow",
		SourceInfo:  ResourceSourceInfo{Source: "npm:@scope/pkg@1.2.3", Scope: "user"},
	}

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.width = 120

	model.input.SetValue("/review")
	if rendered := model.renderSuggestions(); !strings.Contains(rendered, "[p] Review code carefully") {
		t.Fatalf("project source tag missing:\n%s", rendered)
	}
	model.input.SetValue("/skill:g")
	if rendered := model.renderSuggestions(); !strings.Contains(rendered, "[u] Use Go guidance") {
		t.Fatalf("user source tag missing:\n%s", rendered)
	}
	model.input.SetValue("/skill:s")
	if rendered := model.renderSuggestions(); !strings.Contains(rendered, "[u:npm:@scope/pkg@1.2.3] Ship package flow") {
		t.Fatalf("npm source tag missing:\n%s", rendered)
	}
	for _, suggestion := range model.currentSuggestions() {
		if strings.Contains(suggestion, "[") {
			t.Fatalf("source tag leaked into completion value: %#v", model.currentSuggestions())
		}
	}
}

func TestInteractiveExtensionAutocompleteReplacesPrefix(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	api.RegisterAutocompleteProvider(coreext.AutocompleteProviderDefinition{
		Source: "test",
		Suggest: func(_ context.Context, request coreext.AutocompleteRequest) (coreext.AutocompleteSuggestions, error) {
			if !strings.HasSuffix(request.Input, "#1") {
				return coreext.AutocompleteSuggestions{}, nil
			}
			return coreext.AutocompleteSuggestions{
				Prefix: "#1",
				Items: []coreext.AutocompleteItem{{
					Value:       "#123",
					Label:       "#123",
					Description: "Fix login flow",
				}},
			}, nil
		},
	})
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("fix #1")
	suggestions := model.currentSuggestions()
	if !slices.Contains(suggestions, "#123") {
		t.Fatalf("extension suggestion missing: %#v", suggestions)
	}
	if rendered := model.renderSuggestions(); !strings.Contains(rendered, "Fix login flow") {
		t.Fatalf("extension autocomplete description missing:\n%s", rendered)
	}
	if !model.completeSlashCommand() {
		t.Fatal("expected extension completion")
	}
	if got := model.input.Value(); got != "fix #123 " {
		t.Fatalf("completed value=%q want prefix replacement", got)
	}
}

func TestInteractiveExtensionAutocompleteUsesApplyCompletion(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	api.RegisterAutocompleteProvider(coreext.AutocompleteProviderDefinition{
		Source: "test",
		Suggest: func(_ context.Context, request coreext.AutocompleteRequest) (coreext.AutocompleteSuggestions, error) {
			if !strings.HasSuffix(request.Input, "#1") {
				return coreext.AutocompleteSuggestions{}, nil
			}
			return coreext.AutocompleteSuggestions{
				Prefix: "#1",
				Items: []coreext.AutocompleteItem{{
					Value: "#123",
					Label: "#123",
				}},
			}, nil
		},
		Apply: func(_ context.Context, request coreext.AutocompleteApplyRequest) (coreext.AutocompleteApplyResult, error) {
			lines := append([]string(nil), request.Lines...)
			replacement := "issue:" + strings.TrimPrefix(request.Item.Value, "#")
			lines[request.CursorLine] = strings.Replace(lines[request.CursorLine], request.Prefix, replacement+" tail", 1)
			return coreext.AutocompleteApplyResult{Lines: lines, CursorLine: request.CursorLine, CursorCol: len("fix " + replacement)}, nil
		},
	})
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("fix #1")
	if !model.completeSlashCommand() {
		t.Fatal("expected extension completion")
	}
	if got := model.input.Value(); got != "fix issue:123 tail" {
		t.Fatalf("completed value=%q want custom apply result", got)
	}
	if model.input.Line() != 0 || model.input.Column() != len("fix issue:123") {
		t.Fatalf("cursor line/col=%d/%d want 0/%d", model.input.Line(), model.input.Column(), len("fix issue:123"))
	}
}

func numberedLines(count int) string {
	var lines []string
	for i := 1; i <= count; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d", i))
	}
	return strings.Join(lines, "\n")
}

func TestInteractiveInputHistoryNavigation(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.Focus() // so a mis-gated key would actually reach/mutate the textarea
	model.addToHistory("first")
	model.addToHistory("second")
	model.addToHistory("second") // consecutive duplicate is ignored
	model.addToHistory("")       // empty is ignored
	if len(model.history) != 2 || model.history[0] != "second" || model.history[1] != "first" {
		t.Fatalf("history=%#v want [second first]", model.history)
	}

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	send := func(msg tea.Msg) { model.Update(msg) }

	// Up from an empty editor loads the most recent entry, then older.
	send(up)
	if got := model.input.Value(); got != "second" {
		t.Fatalf("after up#1 value=%q want second", got)
	}
	send(up)
	if got := model.input.Value(); got != "first" {
		t.Fatalf("after up#2 value=%q want first", got)
	}
	// Up past the oldest entry is a no-op.
	send(up)
	if got := model.input.Value(); got != "first" {
		t.Fatalf("after up#3 value=%q want first (clamped)", got)
	}
	// Down walks back toward newest, then to an empty editor.
	send(down)
	if got := model.input.Value(); got != "second" {
		t.Fatalf("after down#1 value=%q want second", got)
	}
	send(down)
	if got := model.input.Value(); got != "" {
		t.Fatalf("after down#2 value=%q want empty", got)
	}
	if model.historyIndex != -1 {
		t.Fatalf("historyIndex=%d want -1 after returning to current", model.historyIndex)
	}
}

func TestInteractiveInputHistoryCapAndEditExit(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 130 {
		model.addToHistory(strings.Repeat("x", i+1)) // all distinct, so none skipped
	}
	if len(model.history) != 100 {
		t.Fatalf("history len=%d want 100 (capped)", len(model.history))
	}

	// Browsing, then typing a character exits history-browsing mode. The textarea
	// only edits when focused (Init() focuses it in the real program).
	model.input.Focus()
	model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if model.historyIndex != 0 {
		t.Fatalf("historyIndex=%d want 0 after up", model.historyIndex)
	}
	model.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if model.historyIndex != -1 {
		t.Fatalf("historyIndex=%d want -1 after an edit", model.historyIndex)
	}
}

func TestFileReferenceSuggestions(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"main.go", "README.md", ".hidden", "with space.txt"} {
		if err := os.WriteFile(filepath.Join(cwd, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(cwd, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "src", "foo.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "src", ".env"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	all := fileReferenceSuggestions("@", cwd)
	for _, want := range []string{"@README.md", "@main.go", "@src/", "@\"with space.txt\""} {
		if !slices.Contains(all, want) {
			t.Fatalf("@ suggestions %#v missing %q", all, want)
		}
	}
	if slices.Contains(all, "@.hidden") {
		t.Fatalf("@ suggestions should hide top-level dotfiles: %#v", all)
	}

	if got := fileReferenceSuggestions("@ma", cwd); !slices.Contains(got, "@main.go") || slices.Contains(got, "@README.md") {
		t.Fatalf("@ma suggestions=%#v", got)
	}
	if got := fileReferenceSuggestions("@src/", cwd); !slices.Contains(got, "@src/foo.go") {
		t.Fatalf("@src/ suggestions=%#v", got)
	}
	if got := fileReferenceSuggestions("@src/f", cwd); !slices.Contains(got, "@src/foo.go") {
		t.Fatalf("@src/f suggestions=%#v", got)
	}
	// Dotfiles are surfaced once the user descends into a directory (#12).
	if got := fileReferenceSuggestions("@src/", cwd); !slices.Contains(got, "@src/.env") {
		t.Fatalf("@src/ should surface dotfiles in a subdir: %#v", got)
	}

	// Absolute @-references read the real directory, not cwd, and emit absolute
	// completions (#2). Using cwd's own absolute path exercises the absolute branch.
	absToken := "@" + filepath.Join(cwd, "ma")
	if got := fileReferenceSuggestions(absToken, cwd); !slices.Contains(got, "@"+filepath.ToSlash(filepath.Join(cwd, "main.go"))) {
		t.Fatalf("absolute @ suggestions for %q = %#v", absToken, got)
	}

	// A quoted @-reference may contain spaces; the whole token (from the @) is
	// recovered and the completion is re-quoted (#5).
	token, start, ok := trailingFileRefToken("explain @\"with sp")
	if !ok || start != len("explain ") || token != "@\"with sp" {
		t.Fatalf("trailingFileRefToken quoted: token=%q start=%d ok=%v", token, start, ok)
	}
	if got := fileReferenceSuggestions("@\"with sp", cwd); !slices.Contains(got, "@\"with space.txt\"") {
		t.Fatalf("quoted @ suggestions=%#v want @\"with space.txt\"", got)
	}
}

func TestInteractiveAtCompletionReplacesTrailingToken(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	cwd := runtime.Session().Session.CWD()
	if err := os.WriteFile(filepath.Join(cwd, "notes.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The @-token is completed in place; surrounding text is preserved and a file
	// completion appends a trailing space.
	model.input.SetValue("explain @notes")
	if !model.completeSlashCommand() {
		t.Fatal("expected a completion for @notes")
	}
	if got := model.input.Value(); got != "explain @notes.md " {
		t.Fatalf("completed value=%q want %q", got, "explain @notes.md ")
	}
}

func TestInteractiveAutocompleteDropdownNavigation(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("/")
	if !model.navigateAutocomplete(1) {
		t.Fatal("expected autocomplete navigation to consume down")
	}
	suggestions := model.currentSuggestions()
	if len(suggestions) < 2 {
		t.Fatalf("need at least two suggestions, got %#v", suggestions)
	}
	selected := suggestions[model.selectedSuggestionIndex(suggestions)]
	rendered := model.renderSuggestions()
	if !strings.Contains(rendered, "> ") {
		t.Fatalf("dropdown render should include selected marker, got %q", rendered)
	}
	if !model.completeSlashCommand() {
		t.Fatal("expected completion to apply selected suggestion")
	}
	want := selected
	if !completionIsDirectory(selected) {
		want += " "
	}
	if got := model.input.Value(); got != want {
		t.Fatalf("completed value=%q want selected %q", got, want)
	}
	if model.historyIndex != -1 {
		t.Fatalf("autocomplete navigation should exit history browsing, historyIndex=%d", model.historyIndex)
	}
}

func TestInteractivePathCompletionWithoutAtPrefix(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	cwd := runtime.Session().Session.CWD()
	if err := os.Mkdir(filepath.Join(cwd, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "src", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("inspect src/ma")
	suggestions := model.currentSuggestions()
	if !slices.Contains(suggestions, "src/main.go") {
		t.Fatalf("expected path suggestion, got %#v", suggestions)
	}
	if !model.completeSlashCommand() {
		t.Fatal("expected path completion")
	}
	if got := model.input.Value(); got != "inspect src/main.go " {
		t.Fatalf("completed value=%q want path replacement", got)
	}

	model.input.SetValue("/model faux/f")
	model.autocompleteIndex = 0
	modelSuggestions := model.currentSuggestions()
	if !slices.Contains(modelSuggestions, "/model faux/faux") {
		t.Fatalf("/model suggestions should survive path-looking provider/id, got %#v", modelSuggestions)
	}
}

func TestInteractivePathCompletionQuotedDirectoryContinues(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	cwd := runtime.Session().Session.CWD()
	dir := filepath.Join(cwd, "dir with space")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	model.input.SetValue("inspect \"dir with sp")
	suggestions := model.currentSuggestions()
	if !slices.Contains(suggestions, "\"dir with space/") {
		t.Fatalf("expected open quoted directory suggestion, got %#v", suggestions)
	}
	if !model.completeSlashCommand() {
		t.Fatal("expected quoted directory completion")
	}
	if got := model.input.Value(); got != "inspect \"dir with space/" {
		t.Fatalf("directory completion=%q want open quote without trailing space", got)
	}

	suggestions = model.currentSuggestions()
	if !slices.Contains(suggestions, "\"dir with space/nested.go\"") {
		t.Fatalf("expected nested file suggestion after directory completion, got %#v", suggestions)
	}
}

func TestInteractiveFileCompletionCachesDirectoryReadForSameInput(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	cwd := runtime.Session().Session.CWD()
	if err := os.WriteFile(filepath.Join(cwd, "src.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	oldReadDir := interactiveReadDir
	readCount := 0
	interactiveReadDir = func(name string) ([]os.DirEntry, error) {
		readCount++
		return oldReadDir(name)
	}
	t.Cleanup(func() { interactiveReadDir = oldReadDir })

	model.input.SetValue("@")
	if suggestions := model.currentSuggestions(); !slices.Contains(suggestions, "@src.go") {
		t.Fatalf("expected @ file suggestion, got %#v", suggestions)
	}
	if suggestions := model.currentSuggestions(); !slices.Contains(suggestions, "@src.go") {
		t.Fatalf("expected cached @ file suggestion, got %#v", suggestions)
	}
	if readCount != 1 {
		t.Fatalf("same input should read directory once, got %d reads", readCount)
	}

	model.input.SetValue("@s")
	_ = model.currentSuggestions()
	if readCount != 2 {
		t.Fatalf("changed input should refresh directory suggestions, got %d reads", readCount)
	}
}

func TestLineExtensionUIHandlerAnswersPrompts(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("y\n2\nhello\n"))
	var stdout bytes.Buffer
	handler := newLineExtensionUIHandler(scanner, &stdout)

	result, err := handler(context.Background(), "confirm", json.RawMessage(`{"message":"Run?"}`))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if string(result) != "true" {
		t.Fatalf("confirm result=%s want true", result)
	}

	result, err = handler(context.Background(), "select", json.RawMessage(`{"message":"Pick","choices":["a","b"]}`))
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if string(result) != `"b"` {
		t.Fatalf("select result=%s want %q", result, `"b"`)
	}

	result, err = handler(context.Background(), "input", json.RawMessage(`{"message":"Name"}`))
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if string(result) != `"hello"` {
		t.Fatalf("input result=%s want %q", result, `"hello"`)
	}
}

func TestInteractiveExtensionStatusesSortSanitizeAndClear(t *testing.T) {
	model := &interactiveModel{}
	alpha := " Alpha\nready "
	zeta := " Zeta\tbusy "
	model.setExtensionStatus("zeta", &zeta)
	model.setExtensionStatus("alpha", &alpha)
	if got := model.extensionStatusValues(); len(got) != 2 || got[0] != "Alpha ready" || got[1] != "Zeta busy" {
		t.Fatalf("statuses=%#v", got)
	}
	model.setExtensionStatus("alpha", nil)
	if got := model.extensionStatusValues(); len(got) != 1 || got[0] != "Zeta busy" {
		t.Fatalf("statuses after clear=%#v", got)
	}
}

func TestInteractiveExtensionUISetStatusBindsRuntimeHandler(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	posted := make(chan struct{}, 1)
	model.post = func(msg tea.Msg) {
		model.Update(msg)
		posted <- struct{}{}
	}
	model.bindExtensionUIHandler()
	handler := api.UIHandler()
	if handler == nil {
		t.Fatal("expected interactive model to bind extension UI handler")
	}
	result, err := handler(context.Background(), "setStatus", json.RawMessage(`{"key":"build","text":"Building"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "null" {
		t.Fatalf("result=%s want null", result)
	}
	select {
	case <-posted:
	case <-time.After(time.Second):
		t.Fatal("setStatus message was not posted to the TUI")
	}
	if got := model.extensionStatusValues(); len(got) != 1 || got[0] != "Building" {
		t.Fatalf("statuses=%#v", got)
	}
}

func TestInteractiveExtensionUIConfirmBindsRuntimeHandler(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	posted := make(chan struct{}, 1)
	model.post = func(msg tea.Msg) {
		model.Update(msg)
		posted <- struct{}{}
	}
	model.bindExtensionUIHandler()
	handler := api.UIHandler()
	if handler == nil {
		t.Fatal("expected interactive model to bind extension UI handler")
	}

	type response struct {
		result json.RawMessage
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := handler(context.Background(), "confirm", json.RawMessage(`{"message":"Continue?"}`))
		done <- response{result: result, err: err}
	}()

	select {
	case <-posted:
	case <-time.After(time.Second):
		t.Fatal("extension UI request was not posted to the TUI")
	}
	if model.extensionUI == nil || model.extensionUI.Method != "confirm" {
		t.Fatalf("extensionUI=%#v, want active confirm request", model.extensionUI)
	}
	model.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("handler error: %v", got.err)
		}
		if string(got.result) != "true" {
			t.Fatalf("handler result=%s want true", got.result)
		}
	case <-time.After(time.Second):
		t.Fatal("extension UI handler did not receive the confirm response")
	}
}

func TestInteractiveExtensionTriggerTurnQueuesFollowUpWhileBusy(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	posted := make(chan tea.Msg, 1)
	model.post = func(msg tea.Msg) {
		posted <- msg
	}
	model.bindExtensionUIHandler()
	agent := runtime.Session()
	agent.mu.Lock()
	handler := agent.extensionTriggerTurnHandler
	agent.mu.Unlock()
	if handler == nil {
		t.Fatal("expected interactive model to bind extension triggerTurn handler")
	}
	if err := handler(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-posted:
		if _, ok := msg.(interactiveExtensionTriggerTurnMsg); !ok {
			t.Fatalf("posted msg=%T", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("trigger turn message was not posted to the TUI")
	}

	model.unbindSession()
	model.busy = true
	model.busyKind = interactiveBusyAgent
	cmd := model.handleExtensionTriggerTurn()
	if cmd == nil {
		t.Fatal("expected triggerTurn to return a queue command while busy")
	}
	if done, ok := cmd().(interactiveQueueDoneMsg); !ok || done.Err != nil {
		t.Fatalf("queue result=%#v", done)
	}
	if len(agent.followUpQueue) != 1 || agent.followUpQueue[0].Message != "" {
		t.Fatalf("followUpQueue=%#v", agent.followUpQueue)
	}
}

func testInteractiveRuntime(t *testing.T) *AgentSessionRuntime {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	session := NewAgentSession(InMemorySession(cwd), settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	return &AgentSessionRuntime{
		session: session,
		services: &AgentSessionServices{
			Cwd:             cwd,
			AgentDir:        settings.AgentDir,
			SettingsManager: settings,
			ModelRegistry:   registry,
			ResourceLoader:  resources,
		},
	}
}
