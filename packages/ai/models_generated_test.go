package ai

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

// tsModelSourceCandidates lists the paths we probe for the TypeScript
// source-of-truth catalog, in priority order: an explicit PI_TS_AI_SRC override,
// then the conventional sibling checkout.
func tsModelSourceCandidates() []string {
	var dirs []string
	if env := os.Getenv("PI_TS_AI_SRC"); env != "" {
		dirs = append(dirs, env)
	}
	dirs = append(dirs,
		"/root/guanshan/pi/packages/ai/src",
		"/cbs/guanshan/pi/packages/ai/src",
	)
	return dirs
}

// findTSModelSource returns the first readable models.generated.ts path, or ""
// if none is reachable (in which case the drift check is skipped).
func findTSModelSource() string {
	for _, dir := range tsModelSourceCandidates() {
		path := filepath.Join(dir, "models.generated.ts")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

var (
	tsIDFieldRe       = regexp.MustCompile(`^\s+id: "(.*)",?\s*$`)
	tsProviderFieldRe = regexp.MustCompile(`^\s+provider: "(.*)",?\s*$`)
)

// parseTSModelKeys extracts the (provider, id) set from the TS catalog. Each
// model object in models.generated.ts emits exactly one `id:` line followed a
// few lines later by exactly one `provider:` line, with no nesting, so a simple
// line scan that pairs each id with the next provider is sufficient and stays
// resilient to formatting churn.
func parseTSModelKeys(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open TS catalog: %v", err)
	}
	defer f.Close()

	keys := map[string]bool{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	pendingID := ""
	havePendingID := false
	for scanner.Scan() {
		line := scanner.Text()
		if m := tsIDFieldRe.FindStringSubmatch(line); m != nil {
			pendingID = m[1]
			havePendingID = true
			continue
		}
		if m := tsProviderFieldRe.FindStringSubmatch(line); m != nil && havePendingID {
			keys[m[1]+"\x00"+pendingID] = true
			havePendingID = false
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan TS catalog: %v", err)
	}
	return keys
}

// TestGeneratedTextModelCatalogMatchesTS is a drift check: the Go generated
// catalog's (provider, id) set must equal the TS source-of-truth set. This
// replaces the old `len(...) == 923` magic-number assertion that silently locked
// in 41 missing models / 3 missing providers. When the TS source is unreachable
// (e.g. CI without the sibling checkout) the drift check is skipped, but the
// new-provider presence assertions in TestGeneratedTextModelCatalog still run.
func TestGeneratedTextModelCatalogMatchesTS(t *testing.T) {
	path := findTSModelSource()
	if path == "" {
		t.Skip("TS models.generated.ts not reachable; set PI_TS_AI_SRC to enable the drift check")
	}
	want := parseTSModelKeys(t, path)
	if len(want) == 0 {
		t.Fatalf("parsed 0 models from TS catalog %s — parser likely out of date", path)
	}

	got := map[string]bool{}
	for _, model := range GeneratedModels() {
		got[model.Provider+"\x00"+model.ID] = true
	}

	if len(got) != len(want) {
		t.Errorf("generated model count=%d, TS source-of-truth count=%d (rerun packages/ai/scripts/generate-go-models.ts)", len(got), len(want))
	}
	var missing, extra []string
	for key := range want {
		if !got[key] {
			missing = append(missing, strings.ReplaceAll(key, "\x00", "/"))
		}
	}
	for key := range got {
		if !want[key] {
			extra = append(extra, strings.ReplaceAll(key, "\x00", "/"))
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("models present in TS but missing from Go catalog (%d): %v", len(missing), missing)
	}
	if len(extra) > 0 {
		t.Errorf("models present in Go catalog but not in TS (%d): %v", len(extra), extra)
	}
}

// TestGeneratedTextModelCatalog spot-checks representative models and asserts
// that the three providers added by the 964-model catalog bump (nvidia,
// ant-ling, zai-coding-cn) are present, so a regression that drops them turns
// red even when the TS source is unreachable.
func TestGeneratedTextModelCatalog(t *testing.T) {
	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	seen := map[string]bool{}
	providers := map[string]int{}
	for _, model := range registry.Models {
		key := model.Provider + "\x00" + model.ID
		if seen[key] {
			t.Fatalf("duplicate model in registry: %s/%s", model.Provider, model.ID)
		}
		seen[key] = true
		providers[model.Provider]++
	}
	for _, provider := range []string{"nvidia", "ant-ling", "zai-coding-cn"} {
		if providers[provider] == 0 {
			t.Fatalf("catalog missing provider %q (rerun packages/ai/scripts/generate-go-models.ts)", provider)
		}
	}
	bedrock, ok := registry.Find("amazon-bedrock", "amazon.nova-2-lite-v1:0")
	if !ok {
		t.Fatal("missing generated Bedrock model")
	}
	if bedrock.API != "bedrock-converse-stream" || bedrock.ContextWindow != 128000 || bedrock.Cost.Output != 2.75 {
		t.Fatalf("bedrock model=%#v", bedrock)
	}
	if len(bedrock.ThinkingLevels) != 1 || bedrock.ThinkingLevels[0] != ThinkingOff {
		t.Fatalf("bedrock thinking levels=%#v", bedrock.ThinkingLevels)
	}
	mistral, ok := registry.Find("mistral", "magistral-small")
	if !ok {
		t.Fatal("missing generated Mistral model")
	}
	if mistral.API != "mistral-conversations" || !mistral.Reasoning || !SupportsInput(mistral, "text") {
		t.Fatalf("mistral model=%#v", mistral)
	}
	if mistral.ThinkingLevels[len(mistral.ThinkingLevels)-1] != ThinkingHigh {
		t.Fatalf("mistral thinking levels=%#v", mistral.ThinkingLevels)
	}
	openrouter, ok := registry.Find("openrouter", "mistralai/codestral-2508")
	if !ok {
		t.Fatal("missing generated OpenRouter model")
	}
	if openrouter.API != "openai-completions" || openrouter.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("openrouter model=%#v", openrouter)
	}
}

func TestGeneratedModelBaseURLHelpers(t *testing.T) {
	if got := aiproviders.OpenAIChatURL("https://openrouter.ai/api/v1"); got != "https://openrouter.ai/api/v1/chat/completions" {
		t.Fatalf("openrouter chat URL=%q", got)
	}
	minimax := "https://api.minimax.io/v1/text/chatcompletion_v2"
	if got := aiproviders.OpenAIChatURL(minimax); got != minimax {
		t.Fatalf("minimax chat URL=%q", got)
	}
	if got := aiproviders.AnthropicMessagesURL("https://api.anthropic.com"); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("anthropic messages URL=%q", got)
	}
}
