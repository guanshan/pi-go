package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newExtensionUIModel builds an interactive model sized for deterministic footer
// and View() rendering in the lightweight ctx.ui.* tests.
func newExtensionUIModel(t *testing.T) *interactiveModel {
	t.Helper()
	runtime := testInteractiveRuntime(t)
	m, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	m.width = 80
	m.height = 24
	return m
}

func applyUIState(t *testing.T, m *interactiveModel, method string, params any) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	m.applyExtensionUIState(method, raw)
}

// TestInteractiveExtensionSetTitle verifies ctx.ui.setTitle drives the terminal
// window title via View().WindowTitle.
func TestInteractiveExtensionSetTitle(t *testing.T) {
	m := newExtensionUIModel(t)
	applyUIState(t, m, "setTitle", map[string]any{"title": "Pi Session"})
	if m.windowTitle != "Pi Session" {
		t.Fatalf("windowTitle=%q", m.windowTitle)
	}
	if got := m.View().WindowTitle; got != "Pi Session" {
		t.Fatalf("View().WindowTitle=%q want %q", got, "Pi Session")
	}
}

// TestInteractiveExtensionWorkingState verifies setWorkingMessage / Visible /
// Indicator are reflected in the footer's busy indicator.
func TestInteractiveExtensionWorkingState(t *testing.T) {
	m := newExtensionUIModel(t)
	m.busy = true
	m.busyKind = interactiveBusyAgent

	if got := m.workingFooterStatus(); !strings.HasPrefix(got, "working") {
		t.Fatalf("default working status=%q want working prefix", got)
	}

	applyUIState(t, m, "setWorkingMessage", map[string]any{"message": "Crunching"})
	if got := m.workingFooterStatus(); !strings.HasPrefix(got, "Crunching") {
		t.Fatalf("working status=%q want Crunching prefix", got)
	}

	applyUIState(t, m, "setWorkingIndicator", map[string]any{"frames": []string{"✦", "✧"}, "intervalMs": 100})
	if got := m.workingFooterStatus(); !strings.Contains(got, "✦") {
		t.Fatalf("working status=%q want ✦ glyph", got)
	}
	if m.workingIndicatorInterval != 100 {
		t.Fatalf("workingIndicatorInterval=%d want 100", m.workingIndicatorInterval)
	}

	// Hiding the indicator yields an empty status, omitted from the footer.
	applyUIState(t, m, "setWorkingVisible", map[string]any{"visible": false})
	if got := m.workingFooterStatus(); got != "" {
		t.Fatalf("hidden working status=%q want empty", got)
	}
	if strings.Contains(m.footer(), "Crunching") {
		t.Fatalf("footer still shows working message while hidden: %q", m.footer())
	}

	// Restoring visibility brings the message back.
	applyUIState(t, m, "setWorkingVisible", map[string]any{"visible": true})
	if got := m.workingFooterStatus(); !strings.Contains(got, "Crunching") {
		t.Fatalf("restored working status=%q want Crunching", got)
	}
}

// TestInteractiveExtensionWorkingIndicatorLifecycle verifies the omitted-arg /
// empty-array / custom-frames distinctions for setWorkingIndicator.
func TestInteractiveExtensionWorkingIndicatorLifecycle(t *testing.T) {
	m := newExtensionUIModel(t)

	// Custom frames -> first frame is the glyph.
	applyUIState(t, m, "setWorkingIndicator", map[string]any{"frames": []string{"●"}})
	if !m.workingIndicatorSet || m.workingIndicatorGlyph() != "●" {
		t.Fatalf("custom frames glyph=%q set=%v", m.workingIndicatorGlyph(), m.workingIndicatorSet)
	}

	// Explicit empty array -> set, but no glyph (hidden indicator).
	applyUIState(t, m, "setWorkingIndicator", map[string]any{"frames": []string{}})
	if !m.workingIndicatorSet || m.workingIndicatorGlyph() != "" {
		t.Fatalf("empty frames glyph=%q set=%v want set/empty", m.workingIndicatorGlyph(), m.workingIndicatorSet)
	}

	// Omitted argument ({}) -> restore the default (unset) indicator.
	applyUIState(t, m, "setWorkingIndicator", map[string]any{})
	if m.workingIndicatorSet || m.workingIndicatorGlyph() != "" {
		t.Fatalf("restore default: set=%v glyph=%q want unset/empty", m.workingIndicatorSet, m.workingIndicatorGlyph())
	}
}

// TestInteractiveExtensionHiddenThinkingLabel verifies the label is stored and
// reset by an omitted argument.
func TestInteractiveExtensionHiddenThinkingLabel(t *testing.T) {
	m := newExtensionUIModel(t)
	applyUIState(t, m, "setHiddenThinkingLabel", map[string]any{"label": "[reasoning hidden]"})
	if m.hiddenThinkingLabel != "[reasoning hidden]" {
		t.Fatalf("hiddenThinkingLabel=%q", m.hiddenThinkingLabel)
	}
	applyUIState(t, m, "setHiddenThinkingLabel", map[string]any{})
	if m.hiddenThinkingLabel != "" {
		t.Fatalf("hiddenThinkingLabel=%q want reset to default", m.hiddenThinkingLabel)
	}
}

// TestInteractiveExtensionSetAndPasteEditorText verifies setEditorText replaces
// the editor, pasteToEditor inserts (folding large content), and getEditorText
// returns the paste-expanded text.
func TestInteractiveExtensionSetAndPasteEditorText(t *testing.T) {
	m := newExtensionUIModel(t)

	applyUIState(t, m, "setEditorText", map[string]any{"text": "hello world"})
	if m.input.Value() != "hello world" {
		t.Fatalf("editor=%q want hello world", m.input.Value())
	}
	if got := m.expandedInputText(); got != "hello world" {
		t.Fatalf("expanded=%q", got)
	}

	// Replacing clears prior content.
	applyUIState(t, m, "setEditorText", map[string]any{"text": ""})
	applyUIState(t, m, "pasteToEditor", map[string]any{"text": "short paste"})
	if !strings.Contains(m.input.Value(), "short paste") {
		t.Fatalf("editor after paste=%q", m.input.Value())
	}

	// A large paste folds into a marker but expands back to the original text.
	large := strings.Repeat("a line of pasted content\n", 30)
	applyUIState(t, m, "setEditorText", map[string]any{"text": ""})
	applyUIState(t, m, "pasteToEditor", map[string]any{"text": large})
	if !strings.Contains(m.input.Value(), "[paste #") {
		t.Fatalf("large paste not folded: %q", m.input.Value())
	}
	if !strings.Contains(m.expandedInputText(), "a line of pasted content") {
		t.Fatalf("expanded paste missing original text: %q", m.expandedInputText())
	}
}

// TestInteractiveExtensionUIHandlerRoutesFireAndForget verifies the dispatch
// posts a state message and returns null immediately for a fire-and-forget method.
func TestInteractiveExtensionUIHandlerRoutesFireAndForget(t *testing.T) {
	m := newExtensionUIModel(t)
	captured := make(chan tea.Msg, 1)
	m.post = func(msg tea.Msg) { captured <- msg }

	result, err := m.extensionUIHandler(context.Background(), "setTitle", json.RawMessage(`{"title":"X"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if string(result) != "null" {
		t.Fatalf("result=%s want null", result)
	}
	msg := <-captured
	st, ok := msg.(interactiveExtensionUIStateMsg)
	if !ok || st.Method != "setTitle" {
		t.Fatalf("posted msg=%#v want interactiveExtensionUIStateMsg{setTitle}", msg)
	}
}

// TestInteractiveExtensionUIHandlerGetEditorText verifies the request/response
// round trip reads the current editor text on the Update goroutine.
func TestInteractiveExtensionUIHandlerGetEditorText(t *testing.T) {
	m := newExtensionUIModel(t)
	m.input.SetValue("draft text")
	// Drive the posted message through Update synchronously to model the real
	// Bubble Tea loop replying on the response channel.
	m.post = func(msg tea.Msg) { m.Update(msg) }

	result, err := m.extensionUIHandler(context.Background(), "getEditorText", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got string
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result %s: %v", result, err)
	}
	if got != "draft text" {
		t.Fatalf("getEditorText=%q want draft text", got)
	}
}

// TestInteractiveExtensionSetWidget verifies ctx.ui.setWidget placement, the
// move-on-replacement and remove lifecycle, and the line-count truncation.
func TestInteractiveExtensionSetWidget(t *testing.T) {
	m := newExtensionUIModel(t)

	applyUIState(t, m, "setWidget", map[string]any{"key": "a", "lines": []string{"hello", "world"}, "placement": "aboveEditor"})
	above := m.renderExtensionWidgets("aboveEditor")
	if len(above) != 2 || above[0] != "hello" || above[1] != "world" {
		t.Fatalf("aboveEditor widget=%#v", above)
	}
	if got := m.renderExtensionWidgets("belowEditor"); len(got) != 0 {
		t.Fatalf("belowEditor should be empty, got %#v", got)
	}
	// View() must not panic and should include the widget line.
	if !strings.Contains(m.View().Content, "hello") {
		t.Fatalf("View did not render the aboveEditor widget")
	}

	// Re-setting the same key with a different placement MOVES it (TS parity).
	applyUIState(t, m, "setWidget", map[string]any{"key": "a", "lines": []string{"moved"}, "placement": "belowEditor"})
	if got := m.renderExtensionWidgets("aboveEditor"); len(got) != 0 {
		t.Fatalf("widget should have moved off aboveEditor, got %#v", got)
	}
	if got := m.renderExtensionWidgets("belowEditor"); len(got) != 1 || got[0] != "moved" {
		t.Fatalf("belowEditor widget=%#v", got)
	}

	// Omitting lines removes the widget.
	applyUIState(t, m, "setWidget", map[string]any{"key": "a"})
	if got := m.renderExtensionWidgets("belowEditor"); len(got) != 0 {
		t.Fatalf("widget should be removed, got %#v", got)
	}

	// Over-long content is capped with a truncation marker.
	many := make([]string, 0, 15)
	for i := 0; i < 15; i++ {
		many = append(many, "line")
	}
	applyUIState(t, m, "setWidget", map[string]any{"key": "big", "lines": many})
	got := m.renderExtensionWidgets("aboveEditor")
	if len(got) != interactiveMaxWidgetLines+1 || got[len(got)-1] != "... (widget truncated)" {
		t.Fatalf("truncated widget=%#v (len=%d)", got, len(got))
	}
}

// TestInteractiveExtensionCustomMessageFallback verifies a display custom message
// with no registered renderer renders the default [customType] + content text in
// the live transcript (the previously-missing production path).
func TestInteractiveExtensionCustomMessageFallback(t *testing.T) {
	m := newExtensionUIModel(t)
	m.handleExtensionCustomMessage(interactiveExtensionCustomMessageMsg{
		CustomType: "deploy",
		Content:    "Deploy finished",
	})
	if len(m.messages) == 0 {
		t.Fatal("custom message was not appended to the transcript")
	}
	last := m.messages[len(m.messages)-1]
	if last.Kind != interactiveMessageCustom || last.CustomType != "deploy" {
		t.Fatalf("custom entry=%#v", last)
	}
	joined := strings.Join(m.renderInteractiveMessageLines(last, 80), "\n")
	if !strings.Contains(joined, "[deploy]") || !strings.Contains(joined, "Deploy finished") {
		t.Fatalf("fallback render missing label/content: %q", joined)
	}
}

func TestCustomMessageDisplayText(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string", "hello", "hello"},
		{"text parts", []any{map[string]any{"type": "text", "text": "a"}, map[string]any{"text": "b"}}, "a\nb"},
		{"string parts", []any{"x", "y"}, "x\ny"},
		{"nil", nil, ""},
		{"number", 42, ""},
	}
	for _, tc := range cases {
		if got := customMessageDisplayText(tc.in); got != tc.want {
			t.Errorf("%s: customMessageDisplayText=%q want %q", tc.name, got, tc.want)
		}
	}
}

// TestInteractiveExtensionEditorWithoutEditorErrors verifies ctx.ui.editor
// resolves with an error (not a hang) when no $VISUAL/$EDITOR is configured.
func TestInteractiveExtensionEditorWithoutEditorErrors(t *testing.T) {
	m := newExtensionUIModel(t)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	resp := make(chan extensionUIResult, 1)
	cmd := m.runExtensionEditor("Title", "prefill", resp)
	if cmd != nil {
		t.Fatal("expected nil cmd when no editor is configured")
	}
	select {
	case r := <-resp:
		if r.Err == nil {
			t.Fatal("expected an error result when no editor is configured")
		}
	default:
		t.Fatal("runExtensionEditor did not deliver a result")
	}
}
