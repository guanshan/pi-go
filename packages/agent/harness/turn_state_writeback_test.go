package harness

import (
	"context"
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

// TestCreateTurnStateDoesNotWriteBackSessionModelAndTools verifies the R*-P1
// finding: createTurnState must consume only context.messages and keep using
// the in-memory harness fields (this.model / this.activeToolNames /
// this.thinkingLevel), matching TS agent-harness.ts:331-363. It must NOT write
// the session BuildContext result's model / activeToolNames / thinkingLevel back
// into the harness, and must NOT partially mutate ai.Model (the old bug
// overwrote only Provider/ID from the session's ModelRef, leaving the full
// constructed model's API/ContextWindow stale and polluting model metadata).
//
// Setup: the harness model carries distinctive API/ContextWindow values, while
// the session records a model_change with a DIFFERENT provider/id and an
// active_tools_change with a different tool set. The turn's streamFn must still
// receive the harness's original, fully-populated model (provider/id/api/
// contextWindow unchanged), and GetModel()/GetActiveTools() must be unmutated
// after the turn.
func TestCreateTurnStateDoesNotWriteBackSessionModelAndTools(t *testing.T) {
	ctx := context.Background()
	storage, err := session.NewMemoryStorage(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := session.New(storage)
	// The session carries model/tools metadata that DIFFERS from the live
	// harness state. TS createTurnState deliberately ignores these.
	if _, err := sess.AppendModelChange(ctx, "session-provider", "session-model"); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendActiveToolsChange(ctx, []string{"session-only-tool"}); err != nil {
		t.Fatal(err)
	}

	harnessModel := ai.Model{
		Provider:      "harness-provider",
		ID:            "harness-model",
		API:           "anthropic",
		ContextWindow: 200000,
	}

	var gotModel ai.Model
	h, err := New(Options{
		Session:      sess,
		Model:        harnessModel,
		SystemPrompt: StaticSystemPrompt("system"),
		Tools: []agent.AgentTool{
			namedHarnessTool{name: "alpha"},
			namedHarnessTool{name: "beta"},
		},
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			gotModel = model
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("answer"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.Prompt(ctx, "hello", PromptOptions{}); err != nil {
		t.Fatal(err)
	}

	// streamFn must have received the harness's fully-populated model, NOT the
	// session ModelRef (which would have provider/id from the session and lose
	// the API/ContextWindow).
	if gotModel.Provider != "harness-provider" || gotModel.ID != "harness-model" {
		t.Fatalf("streamFn model identity polluted by session model_change: %+v", gotModel)
	}
	if gotModel.API != "anthropic" || gotModel.ContextWindow != 200000 {
		t.Fatalf("streamFn model metadata clobbered (API/ContextWindow lost): %+v", gotModel)
	}

	// The live harness fields must be unmutated by the turn.
	if got := h.GetModel(); got.Provider != "harness-provider" || got.ID != "harness-model" || got.API != "anthropic" || got.ContextWindow != 200000 {
		t.Fatalf("GetModel mutated by createTurnState writeback: %+v", got)
	}
	active := toolNames(h.GetActiveTools())
	if len(active) != 2 || active[0] != "alpha" || active[1] != "beta" {
		t.Fatalf("GetActiveTools mutated by session active_tools_change writeback: %#v", active)
	}
}
