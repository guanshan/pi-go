package compaction

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

// capturedRequest records the inputs the summarization provider received.
type capturedRequest struct {
	systemPrompt string
	userText     string
	maxTokens    int
	reasoning    ai.ThinkingLevel
}

// capturingProvider records each summarization request and returns a fixed
// (optionally per-call) text response.
type capturingProvider struct {
	mu       sync.Mutex
	requests []capturedRequest
	texts    []string // one response per call; last is reused once exhausted
}

func (p *capturingProvider) API() string { return "capture-test" }

func (p *capturingProvider) Stream(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.handle(req)
}

func (p *capturingProvider) StreamSimple(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.handle(req)
}

func (p *capturingProvider) handle(req ai.ChatRequest) *ai.AssistantMessageEventStream {
	p.mu.Lock()
	idx := len(p.requests)
	p.requests = append(p.requests, capturedRequest{
		systemPrompt: req.SystemPrompt,
		userText:     userText(req.Messages),
		maxTokens:    req.MaxTokens,
		reasoning:    req.ThinkingLevel,
	})
	text := "summary text"
	if len(p.texts) > 0 {
		if idx < len(p.texts) {
			text = p.texts[idx]
		} else {
			text = p.texts[len(p.texts)-1]
		}
	}
	p.mu.Unlock()

	stream := ai.NewAssistantMessageEventStream(2)
	go func() {
		msg := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks(text), ai.Usage{}, "stop")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
	}()
	return stream
}

func (p *capturingProvider) snapshot() []capturedRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]capturedRequest(nil), p.requests...)
}

func userText(messages []ai.Message) string {
	var parts []string
	for _, m := range messages {
		parts = append(parts, ai.MessageText(m))
	}
	return strings.Join(parts, "\n")
}

func registerCapture(t *testing.T) (*capturingProvider, ai.Model, *ai.ModelRegistry) {
	t.Helper()
	provider := &capturingProvider{}
	sourceID := "capture-provider-" + t.Name()
	ai.RegisterProvider(provider, sourceID)
	t.Cleanup(func() { ai.UnregisterProviders(sourceID) })
	model := ai.Model{Provider: "test", ID: "capture-model", API: "capture-test"}
	registry := ai.NewModelRegistry(t.TempDir(), ai.NewAuthStorage(t.TempDir()))
	return provider, model, registry
}

// --- prompt constant parity (mirrors TS exact strings) ---

func TestSummarizationPromptConstantsMatchTS(t *testing.T) {
	if !strings.HasPrefix(summarizationSystemPrompt, "You are a context summarization assistant.") {
		t.Fatalf("system prompt prefix mismatch: %q", summarizationSystemPrompt)
	}
	if !strings.Contains(summarizationSystemPrompt, "Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.") {
		t.Fatalf("system prompt missing 'Do NOT continue' clause")
	}
	for _, want := range []string{
		"The messages above are a conversation to summarize.",
		"## Goal",
		"## Constraints & Preferences",
		"## Progress",
		"### Done",
		"### In Progress",
		"### Blocked",
		"## Key Decisions",
		"## Next Steps",
		"## Critical Context",
		"Keep each section concise. Preserve exact file paths, function names, and error messages.",
	} {
		if !strings.Contains(summarizationPrompt, want) {
			t.Fatalf("SUMMARIZATION_PROMPT missing %q", want)
		}
	}
	for _, want := range []string{
		"The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.",
		"PRESERVE all existing information from the previous summary",
		`UPDATE the Progress section: move items from "In Progress" to "Done" when completed`,
		"## Critical Context",
	} {
		if !strings.Contains(updateSummarizationPrompt, want) {
			t.Fatalf("UPDATE_SUMMARIZATION_PROMPT missing %q", want)
		}
	}
	for _, want := range []string{
		"This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.",
		"## Original Request",
		"## Early Progress",
		"## Context for Suffix",
		"Be concise. Focus on what's needed to understand the kept suffix.",
	} {
		if !strings.Contains(turnPrefixSummarizationPrompt, want) {
			t.Fatalf("TURN_PREFIX_SUMMARIZATION_PROMPT missing %q", want)
		}
	}
}

func TestBranchSummaryPromptConstantMatchesTS(t *testing.T) {
	for _, want := range []string{
		"Create a structured summary of this conversation branch for context when returning later.",
		"## Goal",
		"## Constraints & Preferences",
		"## Progress",
		"### Done",
		"### In Progress",
		"### Blocked",
		"## Key Decisions",
		"## Next Steps",
		"Keep each section concise. Preserve exact file paths, function names, and error messages.",
	} {
		if !strings.Contains(branchSummaryPrompt, want) {
			t.Fatalf("BRANCH_SUMMARY_PROMPT missing %q", want)
		}
	}
	// TS BRANCH_SUMMARY_PROMPT has no "## Critical Context" section (unlike compaction).
	if strings.Contains(branchSummaryPrompt, "## Critical Context") {
		t.Fatalf("BRANCH_SUMMARY_PROMPT should not contain '## Critical Context'")
	}
	if branchSummaryPreamble != "The user explored a different conversation branch before returning here.\nSummary of that exploration:\n\n" {
		t.Fatalf("branch summary preamble mismatch: %q", branchSummaryPreamble)
	}
}

// --- maxTokens math parity (compaction.ts:467-470, 716-719) ---

func TestSummaryMaxTokensMath(t *testing.T) {
	// No maxTokens on model -> floor(fraction * reserveTokens).
	noLimit := ai.Model{}
	if got := summaryMaxTokens(0.8, 16384, noLimit); got != 13107 {
		t.Fatalf("0.8 budget: got %d want 13107", got)
	}
	if got := summaryMaxTokens(0.5, 16384, noLimit); got != 8192 {
		t.Fatalf("0.5 budget: got %d want 8192", got)
	}
	// model.maxTokens caps the budget when smaller.
	capped := ai.Model{MaxOutput: 4096}
	if got := summaryMaxTokens(0.8, 16384, capped); got != 4096 {
		t.Fatalf("capped: got %d want 4096", got)
	}
	// model.maxTokens larger than budget does not raise it.
	big := ai.Model{MaxOutput: 1_000_000}
	if got := summaryMaxTokens(0.8, 16384, big); got != 13107 {
		t.Fatalf("big maxTokens: got %d want 13107", got)
	}
	// Exact TS floor semantics.
	if got := summaryMaxTokens(0.8, 16384, noLimit); got != int(math.Floor(0.8*16384)) {
		t.Fatalf("floor mismatch: %d", got)
	}
}

func TestGenerateSummaryUsesComputedMaxTokensAndSystemPrompt(t *testing.T) {
	provider, model, registry := registerCapture(t)
	_, err := GenerateSummary(context.Background(), []agent.AgentMessage{ai.NewUserMessage("hello", nil)}, model, 16384, "key", nil, "", "", registry, "")
	if err != nil {
		t.Fatal(err)
	}
	reqs := provider.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].maxTokens != 13107 {
		t.Fatalf("maxTokens=%d want 13107", reqs[0].maxTokens)
	}
	if reqs[0].systemPrompt != summarizationSystemPrompt {
		t.Fatalf("system prompt mismatch: %q", reqs[0].systemPrompt)
	}
	if !strings.Contains(reqs[0].userText, "<conversation>") || !strings.Contains(reqs[0].userText, summarizationPrompt) {
		t.Fatalf("user text missing conversation or base prompt: %q", reqs[0].userText)
	}
	if strings.Contains(reqs[0].userText, "<previous-summary>") {
		t.Fatalf("no previous summary expected: %q", reqs[0].userText)
	}
}

func TestGenerateSummaryUpdateFlowInjectsPreviousSummary(t *testing.T) {
	provider, model, registry := registerCapture(t)
	prev := "## Goal\nPrior goal"
	_, err := GenerateSummary(context.Background(), []agent.AgentMessage{ai.NewUserMessage("new work", nil)}, model, 16384, "key", nil, "", prev, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	reqs := provider.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	ut := reqs[0].userText
	if !strings.Contains(ut, "<previous-summary>\n"+prev+"\n</previous-summary>") {
		t.Fatalf("previous summary not injected: %q", ut)
	}
	if !strings.Contains(ut, updateSummarizationPrompt) {
		t.Fatalf("update prompt not used: %q", ut)
	}
	if strings.Contains(ut, summarizationPrompt) && !strings.Contains(updateSummarizationPrompt, summarizationPrompt) {
		t.Fatalf("should use UPDATE prompt, not the create prompt")
	}
}

func TestGenerateSummaryAppendsCustomInstructions(t *testing.T) {
	provider, model, registry := registerCapture(t)
	_, err := GenerateSummary(context.Background(), []agent.AgentMessage{ai.NewUserMessage("work", nil)}, model, 16384, "key", nil, "focus on tests", "", registry, "")
	if err != nil {
		t.Fatal(err)
	}
	reqs := provider.snapshot()
	if !strings.Contains(reqs[0].userText, "Additional focus: focus on tests") {
		t.Fatalf("custom instructions not appended TS-style: %q", reqs[0].userText)
	}
}

// --- split-turn double request flow (compaction.ts:653-695) ---

func TestCompactSplitTurnIssuesTwoRequestsAndJoins(t *testing.T) {
	provider, model, registry := registerCapture(t)
	provider.texts = []string{"HISTORY", "PREFIX"}
	prep := &Preparation{
		FirstKeptEntryID:    "keep",
		MessagesToSummarize: []agent.AgentMessage{ai.NewUserMessage("history msg", nil)},
		TurnPrefixMessages:  []agent.AgentMessage{ai.NewUserMessage("turn prefix msg", nil)},
		IsSplitTurn:         true,
		TokensBefore:        10,
		Settings:            withDefaults(Settings{Enabled: true, SummaryMaxChars: 5000}),
	}
	result, err := Compact(context.Background(), prep, model, "key", nil, "", registry, "")
	if err != nil {
		t.Fatal(err)
	}
	reqs := provider.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 summarization requests, got %d", len(reqs))
	}
	if !strings.Contains(result.Summary, "**Turn Context (split turn):**") {
		t.Fatalf("split-turn join marker missing: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "HISTORY") || !strings.Contains(result.Summary, "PREFIX") {
		t.Fatalf("summary missing history/prefix: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "HISTORY\n\n---\n\n**Turn Context (split turn):**\n\nPREFIX") {
		t.Fatalf("split-turn join format mismatch: %q", result.Summary)
	}
	// One of the two requests must carry the turn-prefix prompt at 0.5 budget.
	foundPrefix := false
	for _, r := range reqs {
		if strings.Contains(r.userText, turnPrefixSummarizationPrompt) {
			foundPrefix = true
			if r.maxTokens != 8192 {
				t.Fatalf("turn prefix maxTokens=%d want 8192", r.maxTokens)
			}
		}
	}
	if !foundPrefix {
		t.Fatalf("no turn-prefix summarization request found")
	}
}

func TestCompactSplitTurnNoHistoryUsesNoPriorHistory(t *testing.T) {
	provider, model, registry := registerCapture(t)
	provider.texts = []string{"PREFIX_ONLY"}
	prep := &Preparation{
		FirstKeptEntryID:    "keep",
		MessagesToSummarize: nil, // no history
		TurnPrefixMessages:  []agent.AgentMessage{ai.NewUserMessage("turn prefix", nil)},
		IsSplitTurn:         true,
		Settings:            withDefaults(Settings{Enabled: true, SummaryMaxChars: 5000}),
	}
	result, err := Compact(context.Background(), prep, model, "key", nil, "", registry, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.snapshot()) != 1 {
		t.Fatalf("expected exactly 1 request (only prefix), got %d", len(provider.snapshot()))
	}
	if !strings.HasPrefix(result.Summary, "No prior history.\n\n---\n\n**Turn Context (split turn):**\n\nPREFIX_ONLY") {
		t.Fatalf("expected 'No prior history.' fallback: %q", result.Summary)
	}
}

// --- branch token-budget boundary (branch-summarization.ts:149-156) ---

func TestPrepareBranchEntriesIncludesOverBudgetSummaryUnder90Percent(t *testing.T) {
	// Build entries: a small recent message plus a large compaction summary
	// entry that pushes us over budget. Because we are still below 90% of the
	// budget when we reach it, the compaction entry must be included.
	bigSummary := strings.Repeat("x", 400) // ~100 tokens
	entries := []session.Entry{
		session.CompactionEntry{Summary: bigSummary, FirstKeptEntryID: "k"},
		session.MessageEntry{Message: ai.NewUserMessage("small recent", nil)},
	}
	// recent message ~3 tokens; compaction ~100 tokens; choose budget so that
	// after the recent message totalTokens < budget*0.9 but adding compaction exceeds it.
	prep := PrepareBranchEntries(entries, 50)
	if len(prep.Messages) != 2 {
		t.Fatalf("expected compaction entry to be included under 90%%, got %d messages", len(prep.Messages))
	}
}

func TestPrepareBranchEntriesExcludesOverBudgetSummaryAbove90Percent(t *testing.T) {
	bigSummary := strings.Repeat("x", 400)
	entries := []session.Entry{
		session.CompactionEntry{Summary: bigSummary, FirstKeptEntryID: "k"},
		session.MessageEntry{Message: ai.NewUserMessage(strings.Repeat("recent ", 20), nil)},
	}
	// Budget tight enough that the recent message alone already pushes
	// totalTokens >= budget*0.9, so the over-budget compaction is NOT included.
	recentTokens := estimateMessageTokens(entries[1].(session.MessageEntry).Message)
	budget := int(float64(recentTokens) / 0.9) // recentTokens >= budget*0.9
	prep := PrepareBranchEntries(entries, budget)
	for _, m := range prep.Messages {
		if _, ok := ai.AsCustomMessage(m); ok {
			// compaction summaries surface as compactionSummary custom messages
			t.Fatalf("over-90%% compaction summary should be excluded, messages=%d", len(prep.Messages))
		}
	}
}

func TestPrepareBranchEntriesPlainMessageOverBudgetNotForciblyIncluded(t *testing.T) {
	// A regular (non-compaction/non-branch_summary) message that exceeds budget
	// is never force-included even when below 90%.
	entries := []session.Entry{
		session.MessageEntry{Message: ai.NewUserMessage(strings.Repeat("big ", 100), nil)},
		session.MessageEntry{Message: ai.NewUserMessage("tiny", nil)},
	}
	prep := PrepareBranchEntries(entries, 5)
	if len(prep.Messages) != 1 {
		t.Fatalf("only the within-budget tail message should be kept, got %d", len(prep.Messages))
	}
}

// --- contextWindow 128000 fallback (branch-summarization.ts:206) ---

func TestGenerateBranchSummaryUsesContextWindowFallback(t *testing.T) {
	provider, model, registry := registerCapture(t)
	provider.texts = []string{"branch body"}
	model.ContextWindow = 0 // no context window -> 128000 fallback
	entries := []session.Entry{
		session.MessageEntry{Message: ai.NewUserMessage("branch work", nil)},
	}
	summary, err := GenerateBranchSummary(context.Background(), entries, BranchSummaryOptions{Model: model, Registry: registry, APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	// With a 128000 fallback budget, the single small message is included and summarized.
	if !strings.Contains(summary.Summary, "branch body") {
		t.Fatalf("expected message summarized under 128000 fallback budget: %q", summary.Summary)
	}
	reqs := provider.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 branch summary request, got %d", len(reqs))
	}
	if reqs[0].maxTokens != 2048 {
		t.Fatalf("branch summary maxTokens=%d want 2048", reqs[0].maxTokens)
	}
	if reqs[0].systemPrompt != summarizationSystemPrompt {
		t.Fatalf("branch summary should use SUMMARIZATION_SYSTEM_PROMPT")
	}
	if !strings.Contains(reqs[0].userText, branchSummaryPrompt) {
		t.Fatalf("branch summary prompt not used: %q", reqs[0].userText)
	}
}

func TestGenerateBranchSummaryEmptyBudgetWithFallbackStillSummarizes(t *testing.T) {
	// reserveTokens default 16384 < 128000 fallback, so tokenBudget is positive
	// and content is included (previously a zero contextWindow yielded budget 0).
	provider, model, registry := registerCapture(t)
	provider.texts = []string{"ok"}
	model.ContextWindow = 0
	entries := []session.Entry{
		session.MessageEntry{Message: ai.NewUserMessage("hello branch", nil)},
	}
	summary, err := GenerateBranchSummary(context.Background(), entries, BranchSummaryOptions{Model: model, Registry: registry, ReserveTokens: 16384})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Summary == "No content to summarize" {
		t.Fatalf("content should be summarized with 128000 fallback budget")
	}
	if len(provider.snapshot()) != 1 {
		t.Fatalf("expected the model to be invoked once")
	}
}
