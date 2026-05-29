package harness

import (
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

func TestConvertToLLM(t *testing.T) {
	exitCode := 7
	messages := []agent.AgentMessage{
		ai.CustomMessage{Role: "bashExecution", Command: "make test", Output: "fail", ExitCode: &exitCode, TimestampMs: 1},
		CreateBranchSummaryMessage("branch work", "a1", "2026-05-27T00:00:00Z"),
		CreateCompactionSummaryMessage("old work", 123, "2026-05-27T00:00:00Z"),
	}
	llm, err := ConvertToLLM(messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(llm) != 3 {
		t.Fatalf("llm len=%d", len(llm))
	}
	if got := ai.MessageText(llm[0]); !strings.Contains(got, "Command exited with code 7") {
		t.Fatalf("bash text=%q", got)
	}
	if got := ai.MessageText(llm[1]); !strings.Contains(got, BranchSummaryPrefix+"branch work") {
		t.Fatalf("branch text=%q", got)
	}
	if got := ai.MessageText(llm[2]); !strings.Contains(got, CompactionSummaryPrefix+"old work") {
		t.Fatalf("compaction text=%q", got)
	}
}

type externalHarnessMessage struct {
	role      string
	timestamp int64
}

func (m externalHarnessMessage) MessageRole() string { return m.role }
func (m externalHarnessMessage) Timestamp() int64    { return m.timestamp }

var _ agent.AgentMessage = externalHarnessMessage{}

func TestExternalMessageCanImplementAgentMessage(t *testing.T) {
	msg := externalHarnessMessage{role: "external", timestamp: 42}
	if ai.MessageRole(msg) != "external" || ai.MessageTimestamp(msg) != 42 {
		t.Fatalf("message helpers did not use exported methods")
	}
	llm, err := ConvertToLLM([]agent.AgentMessage{msg})
	if err != nil {
		t.Fatal(err)
	}
	if len(llm) != 0 {
		t.Fatalf("unknown external message should be skipped by default: %#v", llm)
	}
}
