package core

import (
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// TestGetUserMessagesForForkingIncludesOtherBranches verifies fork points are
// gathered from the whole session tree (TS getUserMessagesForForking over
// getEntries()), not only the current branch — so a user message on an
// abandoned/sibling branch is still offered as a fork point and can be cloned.
func TestGetUserMessagesForForkingIncludesOtherBranches(t *testing.T) {
	cwd := t.TempDir()
	session := InMemorySession(cwd)
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model := ai.Model{Provider: "unit", ID: "fork-model", API: "fork-api", MaxOutput: 2048}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}

	rootID := appendSessionMessage(t, session, ai.NewUserMessage("first", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("reply"), ai.Usage{}, "stop"))
	otherBranchID := appendSessionMessage(t, session, ai.NewUserMessage("second", nil)) // branch A leaf
	// Branch off the root so the current branch becomes [first, alt]; "second"
	// now lives only on the abandoned branch A.
	if err := session.SetLeaf(rootID); err != nil {
		t.Fatal(err)
	}
	appendSessionMessage(t, session, ai.NewUserMessage("alt", nil))

	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")

	byID := map[string]string{}
	for _, msg := range agent.GetUserMessagesForForking() {
		byID[msg.EntryID] = msg.Text
	}

	texts := map[string]bool{}
	for _, text := range byID {
		texts[text] = true
	}
	for _, want := range []string{"first", "second", "alt"} {
		if !texts[want] {
			t.Fatalf("fork messages missing %q; got %v", want, byID)
		}
	}
	// The off-branch message is the regression target: the old Branch()-based
	// implementation dropped it.
	if byID[otherBranchID] != "second" {
		t.Fatalf("off-branch fork point not surfaced: %v", byID)
	}

	// And it must be forkable: cloning at its entry yields a branch ending there.
	clone, err := CloneSessionBranch(session, otherBranchID, t.TempDir())
	if err != nil {
		t.Fatalf("clone at off-branch fork point: %v", err)
	}
	last := clone.Entries[len(clone.Entries)-1]
	if got := ai.MessageText(last.Message); got != "second" {
		t.Fatalf("cloned branch leaf text=%q, want %q", got, "second")
	}
}
