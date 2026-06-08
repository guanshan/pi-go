package ai

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

func TestTransformMessagesDowngradesAndRepairsConversation(t *testing.T) {
	model := Model{Provider: "openai", ID: "gpt-text", API: "openai-completions", Input: []string{"text"}}
	assistant := NewAssistantMessage("other-api", "other", "foreign-model", []ContentBlock{
		{Type: "thinking", Thinking: "scratch", Signature: "foreign-sig"},
		{Type: "toolCall", ID: "call|1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), ThoughtSignature: "thought"},
	}, Usage{}, "toolUse")
	messages := []Message{
		NewUserMessage("look", []ContentBlock{{Type: "image", MimeType: "image/png", Data: "abc"}}),
		assistant,
		NewUserMessage("next", nil),
	}

	got := transformMessages(messages, model, func(id string, _ Model, _ AssistantMessage) string {
		if id == "call|1" {
			return "call_1"
		}
		return id
	})

	user := got[0].(UserMessage)
	if len(user.Content) != 2 || user.Content[1].Text != nonVisionUserImagePlaceholder {
		t.Fatalf("user content=%#v", user.Content)
	}
	assistantOut := got[1].(AssistantMessage)
	if len(assistantOut.Content) != 2 || assistantOut.Content[0].Type != "text" || assistantOut.Content[0].Text != "scratch" {
		t.Fatalf("assistant content=%#v", assistantOut.Content)
	}
	if assistantOut.Content[1].ID != "call_1" || assistantOut.Content[1].ThoughtSignature != "" {
		t.Fatalf("tool call=%#v", assistantOut.Content[1])
	}
	repair := got[2].(ToolResultMessage)
	if repair.ToolCallID != "call_1" || !repair.IsError || MessageText(repair) != "No result provided" {
		t.Fatalf("repair=%#v", repair)
	}
}

func TestOpenAIResponsesThinkingUsesRawItem(t *testing.T) {
	raw := json.RawMessage(`{"type":"reasoning","id":"rs_1"}`)
	block := openAIResponseBlock(aiproviders.OpenAIResponsesBlock{Type: "thinking", Thinking: "summary", RawItem: raw})
	if string(block.RawItem) != string(raw) || block.Signature != "" {
		t.Fatalf("block=%#v", block)
	}
}

func TestTransformMessagesNormalizesCustomSessionMessages(t *testing.T) {
	model := Model{Provider: "openai", ID: "gpt-vision", API: "openai-completions", Input: []string{"text", "image"}}
	exitCode := 7
	messages := []Message{
		CustomMessage{Role: "branchSummary", Summary: "branch work", TimestampMs: 1},
		CustomMessage{Role: "compactionSummary", Summary: "old work", TimestampMs: 2},
		CustomMessage{Role: "bashExecution", Command: "make test", Output: "fail", ExitCode: &exitCode, TimestampMs: 3},
		CustomMessage{Role: "bashExecution", Command: "pwd", Output: "/tmp", ExcludeFromContext: true, TimestampMs: 4},
		CustomMessage{
			Role:       "custom",
			CustomType: "note",
			Content: []interface{}{
				map[string]interface{}{"type": "text", "text": "look at this screenshot"},
				map[string]interface{}{"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"},
			},
			TimestampMs: 5,
		},
	}

	got := transformMessages(messages, model, nil)
	if len(got) != 4 {
		t.Fatalf("transformed len=%d, want 4: %#v", len(got), got)
	}
	for i, msg := range got {
		if role := MessageRole(msg); role != "user" {
			t.Fatalf("message %d role=%q, want user (%#v)", i, role, msg)
		}
	}
	if got := MessageText(got[0]); got != BranchSummaryText("branch work") {
		t.Fatalf("branch summary text=%q", got)
	}
	if got := MessageText(got[1]); got != CompactionSummaryText("old work") {
		t.Fatalf("compaction summary text=%q", got)
	}
	if got := MessageText(got[2]); !strings.Contains(got, "Ran `make test`") || !strings.Contains(got, "Command exited with code 7") {
		t.Fatalf("bash execution text=%q", got)
	}
	blocks := MessageBlocks(got[3])
	if len(blocks) != 2 || blocks[0].Text != "look at this screenshot" || blocks[1].Type != "image" || blocks[1].Data != "aGVsbG8=" {
		t.Fatalf("custom content blocks not preserved: %#v", blocks)
	}
}

func TestTransformMessagesConvertsScalarCustomContentToText(t *testing.T) {
	model := Model{Provider: "openai", ID: "gpt-text", API: "openai-completions", Input: []string{"text"}}
	got := transformMessages([]Message{
		CustomMessage{Role: "custom", CustomType: "number", Content: float64(7), TimestampMs: 1},
		CustomMessage{Role: "custom", CustomType: "bool", Content: true, TimestampMs: 2},
	}, model, nil)
	if len(got) != 2 {
		t.Fatalf("transformed len=%d, want 2: %#v", len(got), got)
	}
	if text := MessageText(got[0]); text != "7" {
		t.Fatalf("number custom text=%q", text)
	}
	if text := MessageText(got[1]); text != "true" {
		t.Fatalf("bool custom text=%q", text)
	}
}

func TestTransformMessagesPreservesUnstructuredCustomContentAsText(t *testing.T) {
	model := Model{Provider: "openai", ID: "gpt-text", API: "openai-completions", Input: []string{"text"}}
	got := transformMessages([]Message{
		CustomMessage{Role: "custom", CustomType: "object-array", Content: []any{map[string]any{"foo": "bar"}}, TimestampMs: 1},
	}, model, nil)
	if len(got) != 1 {
		t.Fatalf("transformed len=%d, want 1: %#v", len(got), got)
	}
	if role := MessageRole(got[0]); role != "user" {
		t.Fatalf("role=%q", role)
	}
	if text := MessageText(got[0]); !strings.Contains(text, `"foo":"bar"`) {
		t.Fatalf("custom content text=%q", text)
	}
	for _, block := range MessageBlocks(got[0]) {
		if block.Type == "" {
			t.Fatalf("empty block leaked: %#v", MessageBlocks(got[0]))
		}
	}
}

func TestProviderAdaptersNormalizeCustomSessionRoles(t *testing.T) {
	model := Model{Provider: "openai", ID: "gpt-text", API: "openai-completions", Input: []string{"text"}}
	messages := []Message{CustomMessage{Role: "branchSummary", Summary: "branch work", TimestampMs: 1}}

	chat := openAIChatMessages(messages, model)
	if len(chat) != 1 || chat[0].Role != "user" || chat[0].Text != BranchSummaryText("branch work") {
		t.Fatalf("openai chat messages=%#v", chat)
	}
	responses := openAIResponsesMessages(messages, model)
	if len(responses) != 1 || responses[0].Role != "user" || responses[0].Text != BranchSummaryText("branch work") {
		t.Fatalf("openai responses messages=%#v", responses)
	}
}

type providerContentTestMessage struct {
	role      string
	timestamp int64
	blocks    []ContentBlock
}

func (m providerContentTestMessage) MessageRole() string { return m.role }
func (m providerContentTestMessage) Timestamp() int64    { return m.timestamp }
func (m providerContentTestMessage) ProviderContentBlocks() []ContentBlock {
	return m.blocks
}

func TestTransformMessagesNormalizesProviderContentMessages(t *testing.T) {
	model := Model{Provider: "openai", ID: "gpt-text", API: "openai-completions", Input: []string{"text"}}
	messages := []Message{providerContentTestMessage{
		role:      "branchSummary",
		timestamp: 11,
		blocks:    TextBlocks("provider-facing context"),
	}}

	got := transformMessages(messages, model, nil)
	if len(got) != 1 || MessageRole(got[0]) != "user" || MessageText(got[0]) != "provider-facing context" || MessageTimestamp(got[0]) != 11 {
		t.Fatalf("normalized provider content message=%#v", got)
	}
}

// anthropicNormalizeToolCallID mirrors the normalizer anthropic.ts hands to
// transformMessages: replace any char outside ^[a-zA-Z0-9_-]+$ with "_" and
// clamp to 64 chars. Used by the handoff tests below.
var anthropicToolCallIDInvalid = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func anthropicNormalizeToolCallID(id string, _ Model, _ AssistantMessage) string {
	cleaned := anthropicToolCallIDInvalid.ReplaceAllString(id, "_")
	if len(cleaned) > 64 {
		cleaned = cleaned[:64]
	}
	return cleaned
}

// copilotClaudeModel mirrors makeCopilotClaudeModel from
// transform-messages-copilot-openai-to-anthropic.test.ts: a github-copilot
// model whose API is anthropic-messages.
func copilotClaudeModel() Model {
	return Model{
		ID:       "claude-sonnet-4.6",
		Name:     "Claude Sonnet 4.6",
		API:      "anthropic-messages",
		Provider: "github-copilot",
		Input:    []string{"text", "image"},
	}
}

func findAssistant(t *testing.T, msgs []Message) AssistantMessage {
	t.Helper()
	for _, m := range msgs {
		if a, ok := m.(AssistantMessage); ok {
			return a
		}
	}
	t.Fatalf("no assistant message in %#v", msgs)
	return AssistantMessage{}
}

// Mirrors "converts thinking blocks to plain text when source model differs".
// The historical assistant was produced by a DIFFERENT model
// (openai-completions vs anthropic-messages), so its thinking block must be
// downgraded to plain text rather than replayed.
func TestTransformMessagesConvertsThinkingToTextWhenModelDiffers(t *testing.T) {
	model := copilotClaudeModel()
	assistant := AssistantMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "thinking", Thinking: "Let me think about this...", ThinkingSignature: "reasoning_content"},
			{Type: "text", Text: "Hi there!"},
		},
		API:        "openai-completions",
		Provider:   "github-copilot",
		Model:      "gpt-4o",
		StopReason: "stop",
	}
	messages := []Message{NewUserMessage("hello", nil), assistant}

	got := transformMessages(messages, model, anthropicNormalizeToolCallID)
	out := findAssistant(t, got)

	thinking := 0
	text := 0
	for _, b := range out.Content {
		switch b.Type {
		case "thinking":
			thinking++
		case "text":
			text++
		}
	}
	if thinking != 0 {
		t.Fatalf("expected thinking blocks downgraded, got content=%#v", out.Content)
	}
	if text < 2 {
		t.Fatalf("expected >=2 text blocks after downgrade, got content=%#v", out.Content)
	}
}

// Mirrors "removes thoughtSignature from tool calls when migrating between
// models".
func TestTransformMessagesRemovesThoughtSignatureCrossModel(t *testing.T) {
	model := copilotClaudeModel()
	thought, _ := json.Marshal(map[string]string{"type": "reasoning.encrypted", "id": "call_123", "data": "encrypted"})
	assistant := AssistantMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "toolCall", ID: "call_123", Name: "bash", Arguments: json.RawMessage(`{"command":"ls"}`), ThoughtSignature: string(thought)},
		},
		API:        "openai-responses",
		Provider:   "github-copilot",
		Model:      "gpt-5",
		StopReason: "toolUse",
	}
	toolResult := ToolResultMessage{Role: "toolResult", ToolCallID: "call_123", ToolName: "bash", Content: TextBlocks("output")}
	messages := []Message{NewUserMessage("run a command", nil), assistant, toolResult}

	got := transformMessages(messages, model, anthropicNormalizeToolCallID)
	out := findAssistant(t, got)

	var call *ContentBlock
	for i := range out.Content {
		if out.Content[i].Type == "toolCall" {
			call = &out.Content[i]
		}
	}
	if call == nil {
		t.Fatalf("no toolCall block in %#v", out.Content)
	}
	if call.ThoughtSignature != "" {
		t.Fatalf("expected thoughtSignature cleared cross-model, got %q", call.ThoughtSignature)
	}
}

// Mirrors "adds synthetic tool results for trailing orphaned tool calls": a
// pipe-separated OpenAI Responses ID is normalized to anthropic form, and a
// synthetic error tool result is appended for the orphaned call.
func TestTransformMessagesRepairsTrailingOrphanedToolCall(t *testing.T) {
	model := copilotClaudeModel()
	assistant := AssistantMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "toolCall", ID: "call_123|fc_123", Name: "read", Arguments: json.RawMessage(`{"path":"README.md"}`)},
		},
		API:        "openai-responses",
		Provider:   "github-copilot",
		Model:      "gpt-5",
		StopReason: "toolUse",
	}
	messages := []Message{NewUserMessage("read the file", nil), assistant}

	got := transformMessages(messages, model, anthropicNormalizeToolCallID)
	last, ok := got[len(got)-1].(ToolResultMessage)
	if !ok {
		t.Fatalf("expected trailing toolResult, got %#v", got[len(got)-1])
	}
	if last.ToolCallID != "call_123_fc_123" || last.ToolName != "read" || !last.IsError {
		t.Fatalf("synthetic result mismatch: %#v", last)
	}
	if MessageText(last) != "No result provided" {
		t.Fatalf("synthetic result content=%q", MessageText(last))
	}
}

// Mirrors "adds synthetic results only for trailing tool calls that are still
// missing results": only the second (un-resulted) call gets a synthetic
// result, and the tool-call-id of the synthetic result is normalized.
func TestTransformMessagesRepairsOnlyMissingResults(t *testing.T) {
	model := copilotClaudeModel()
	assistant := AssistantMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "toolCall", ID: "call_1|fc_1", Name: "read", Arguments: json.RawMessage(`{"path":"README.md"}`)},
			{Type: "toolCall", ID: "call_2|fc_2", Name: "bash", Arguments: json.RawMessage(`{"command":"pwd"}`)},
		},
		API:        "openai-responses",
		Provider:   "github-copilot",
		Model:      "gpt-5",
		StopReason: "toolUse",
	}
	// Tool result for the first call uses the pre-normalization ID; the
	// transform must map it to the normalized ID so it is recognized as
	// satisfying the first call.
	toolResult := ToolResultMessage{Role: "toolResult", ToolCallID: "call_1|fc_1", ToolName: "read", Content: TextBlocks("done")}
	messages := []Message{NewUserMessage("run commands", nil), assistant, toolResult}

	got := transformMessages(messages, model, anthropicNormalizeToolCallID)

	var synthetic []ToolResultMessage
	for _, m := range got {
		if tr, ok := m.(ToolResultMessage); ok && tr.IsError {
			synthetic = append(synthetic, tr)
		}
	}
	if len(synthetic) != 1 {
		t.Fatalf("expected exactly 1 synthetic result, got %d (%#v)", len(synthetic), synthetic)
	}
	if synthetic[0].ToolCallID != "call_2_fc_2" || synthetic[0].ToolName != "bash" {
		t.Fatalf("synthetic result mismatch: %#v", synthetic[0])
	}
	if MessageText(synthetic[0]) != "No result provided" {
		t.Fatalf("synthetic result content=%q", MessageText(synthetic[0]))
	}
}

// P1-A2: an assistant message whose API == "" must be treated as a DIFFERENT
// model (no loose empty-API escape hatch). Encrypted/redacted reasoning is
// dropped, plain thinking is downgraded to text, signatures and
// thoughtSignature are stripped, and the tool-call ID is normalized.
func TestTransformMessagesEmptyAPIIsDifferentModel(t *testing.T) {
	model := Model{Provider: "openai", ID: "gpt-5", API: "openai-responses", Input: []string{"text"}}
	assistant := AssistantMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "thinking", Thinking: "", Signature: "encrypted-reasoning"},
			{Type: "thinking", Redacted: true, Signature: "opaque"},
			{Type: "thinking", Thinking: "visible reasoning", Signature: "sig"},
			{Type: "toolCall", ID: "call|abc", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), ThoughtSignature: "thought"},
		},
		// API deliberately empty: same provider+model as the target, but an
		// empty api must NOT be treated as the same model.
		API:        "",
		Provider:   "openai",
		Model:      "gpt-5",
		StopReason: "toolUse",
	}
	messages := []Message{NewUserMessage("hi", nil), assistant, NewUserMessage("next", nil)}

	got := transformMessages(messages, model, func(id string, _ Model, _ AssistantMessage) string {
		if id == "call|abc" {
			return "call_abc"
		}
		return id
	})
	out := findAssistant(t, got)

	for _, b := range out.Content {
		if b.Type == "thinking" {
			t.Fatalf("empty-API message treated as same model; thinking not downgraded: %#v", out.Content)
		}
	}
	// Empty-signature thinking dropped, redacted dropped, visible thinking -> text.
	var texts []string
	var call *ContentBlock
	for i := range out.Content {
		switch out.Content[i].Type {
		case "text":
			texts = append(texts, out.Content[i].Text)
		case "toolCall":
			call = &out.Content[i]
		}
	}
	if len(texts) != 1 || texts[0] != "visible reasoning" {
		t.Fatalf("expected only visible reasoning downgraded to text, got texts=%#v content=%#v", texts, out.Content)
	}
	if call == nil || call.ID != "call_abc" || call.ThoughtSignature != "" {
		t.Fatalf("tool call not normalized/cleared: %#v", call)
	}
}

// P1-A2: an assistant message whose API equals the provider NAME (the old
// loose escape hatch) must be treated as a DIFFERENT model.
func TestTransformMessagesAPIEqualsProviderNameIsDifferentModel(t *testing.T) {
	model := Model{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic-messages", Input: []string{"text"}}
	assistant := AssistantMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "thinking", Thinking: "secret reasoning", Signature: "anthropic-sig"},
			{Type: "text", Text: "answer"},
		},
		// api == provider name; under the old loose check this matched.
		API:        "anthropic",
		Provider:   "anthropic",
		Model:      "claude-sonnet-4-5",
		StopReason: "stop",
	}
	messages := []Message{NewUserMessage("hi", nil), assistant}

	got := transformMessages(messages, model, anthropicNormalizeToolCallID)
	out := findAssistant(t, got)

	for _, b := range out.Content {
		if b.Type == "thinking" {
			t.Fatalf("api==provider treated as same model; thinking/signature replayed: %#v", out.Content)
		}
	}
	if len(out.Content) != 2 || out.Content[0].Type != "text" || out.Content[0].Text != "secret reasoning" {
		t.Fatalf("expected thinking downgraded to text, got %#v", out.Content)
	}
	if out.Content[1].Type != "text" || out.Content[1].Text != "answer" {
		t.Fatalf("expected text preserved, got %#v", out.Content)
	}
}

// P1-A2 / P1-H5(d): when the assistant message was produced by exactly the
// same model (provider+api+id all equal), thinking blocks and their
// signatures are preserved for replay (including OpenAI-style encrypted
// reasoning whose thinking text is empty), and tool-call IDs / thoughtSignature
// are left untouched.
func TestTransformMessagesSameModelPreservesReasoning(t *testing.T) {
	model := Model{Provider: "openai", ID: "gpt-5", API: "openai-responses", Input: []string{"text"}}
	raw := json.RawMessage(`{"type":"reasoning","id":"rs_1"}`)
	assistant := AssistantMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "thinking", Thinking: "", Signature: "encrypted-reasoning", RawItem: raw},
			{Type: "thinking", Redacted: true, Signature: "opaque"},
			{Type: "toolCall", ID: "call|abc", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), ThoughtSignature: "thought"},
		},
		API:        "openai-responses",
		Provider:   "openai",
		Model:      "gpt-5",
		StopReason: "toolUse",
	}
	toolResult := ToolResultMessage{Role: "toolResult", ToolCallID: "call|abc", ToolName: "lookup", Content: TextBlocks("ok")}
	messages := []Message{NewUserMessage("hi", nil), assistant, toolResult}

	got := transformMessages(messages, model, func(id string, _ Model, _ AssistantMessage) string {
		t.Fatalf("normalizer must not run for same model (id=%q)", id)
		return id
	})
	out := findAssistant(t, got)

	var encrypted, redacted, call *ContentBlock
	for i := range out.Content {
		switch {
		case out.Content[i].Type == "thinking" && out.Content[i].Redacted:
			redacted = &out.Content[i]
		case out.Content[i].Type == "thinking":
			encrypted = &out.Content[i]
		case out.Content[i].Type == "toolCall":
			call = &out.Content[i]
		}
	}
	if encrypted == nil || encrypted.Signature != "encrypted-reasoning" || string(encrypted.RawItem) != string(raw) {
		t.Fatalf("expected encrypted reasoning preserved for same model, got %#v", out.Content)
	}
	if redacted == nil || redacted.Signature != "opaque" {
		t.Fatalf("expected redacted thinking preserved for same model, got %#v", out.Content)
	}
	if call == nil || call.ID != "call|abc" || call.ThoughtSignature != "thought" {
		t.Fatalf("expected tool call untouched for same model, got %#v", call)
	}
}

// P1-H5(b): tool-call-id normalization is applied consistently to the assistant
// tool call AND any matching tool result on cross-provider handoff, so the
// orphan-repair pass recognizes the pre-normalization result as satisfying the
// normalized call (no spurious synthetic result).
func TestTransformMessagesNormalizesToolCallIDAcrossProviders(t *testing.T) {
	model := copilotClaudeModel()
	longID := "call_xyz|AAAA+/=ZZZ"
	assistant := AssistantMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "toolCall", ID: longID, Name: "echo", Arguments: json.RawMessage(`{"message":"hi"}`)},
		},
		API:        "openai-responses",
		Provider:   "github-copilot",
		Model:      "gpt-5.2-codex",
		StopReason: "toolUse",
	}
	toolResult := ToolResultMessage{Role: "toolResult", ToolCallID: longID, ToolName: "echo", Content: TextBlocks("hi")}
	messages := []Message{NewUserMessage("echo hi", nil), assistant, toolResult, NewUserMessage("thanks", nil)}

	got := transformMessages(messages, model, anthropicNormalizeToolCallID)
	normalized := anthropicNormalizeToolCallID(longID, model, assistant)

	out := findAssistant(t, got)
	if len(out.Content) != 1 || out.Content[0].ID != normalized {
		t.Fatalf("assistant tool call id not normalized: %#v (want %q)", out.Content, normalized)
	}
	if anthropicToolCallIDInvalid.MatchString(normalized) {
		t.Fatalf("normalized id still has invalid chars: %q", normalized)
	}

	var resultIDs []string
	var syntheticCount int
	for _, m := range got {
		if tr, ok := m.(ToolResultMessage); ok {
			resultIDs = append(resultIDs, tr.ToolCallID)
			if tr.IsError {
				syntheticCount++
			}
		}
	}
	if syntheticCount != 0 {
		t.Fatalf("expected no synthetic results (result was provided), got %d (%v)", syntheticCount, resultIDs)
	}
	if len(resultIDs) != 1 || resultIDs[0] != normalized {
		t.Fatalf("tool result id not remapped to normalized id: %v (want %q)", resultIDs, normalized)
	}
}
