package providers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestProviderMessagesPreserveCustomRolesAsUser(t *testing.T) {
	const text = "summary carried through direct provider call"

	anthropicOut := AnthropicMessages([]AnthropicMessage{{Role: "branchSummary", Text: text}}, anthropic.CacheControlEphemeralParam{}, false, false, false)
	if len(anthropicOut) != 1 || string(anthropicOut[0].Role) != "user" || !jsonContains(t, anthropicOut[0], text) {
		t.Fatalf("anthropic messages=%#v", anthropicOut)
	}

	bedrockOut := BedrockMessages([]BedrockMessage{{Role: "compactionSummary", Text: text}}, "", "", "none")
	if len(bedrockOut) != 1 || string(bedrockOut[0].Role) != "user" {
		t.Fatalf("bedrock messages=%#v", bedrockOut)
	}
	bedrockText, ok := bedrockOut[0].Content[0].(*bedrocktypes.ContentBlockMemberText)
	if !ok || bedrockText.Value != text {
		t.Fatalf("bedrock content=%#v", bedrockOut[0].Content)
	}

	mistralOut := MistralMessages("", []MistralMessage{{Role: "custom", Text: text}}, false)
	if len(mistralOut) != 1 || mistralOut[0]["role"] != "user" || mistralOut[0]["content"] != text {
		t.Fatalf("mistral messages=%#v", mistralOut)
	}

	chatOut := OpenAIChatMessages(OpenAIChatRequestOptions{Messages: []OpenAIChatMessage{{Role: "branchSummary", Text: text}}})
	if len(chatOut) != 1 || chatOut[0]["role"] != "user" || chatOut[0]["content"] != text {
		t.Fatalf("openai chat messages=%#v", chatOut)
	}

	responsesOut := ResponsesMessages(OpenAIResponsesRequestOptions{Messages: []OpenAIResponsesMessage{{Role: "branchSummary", Text: text}}}, false)
	if len(responsesOut) != 1 || responsesOut[0]["role"] != "user" || !jsonContains(t, responsesOut[0], text) {
		t.Fatalf("openai responses messages=%#v", responsesOut)
	}
}

func jsonContains(t *testing.T, value any, needle string) bool {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %#v: %v", value, err)
	}
	return strings.Contains(string(raw), needle)
}
