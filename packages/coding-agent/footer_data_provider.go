package codingagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type gitPaths struct {
	repoDir      string
	commonGitDir string
	headPath     string
}

type FooterDataProvider struct {
	mu                     sync.Mutex
	cwd                    string
	extensionStatuses      map[string]string
	cachedBranch           string
	branchResolved         bool
	gitPaths               *gitPaths
	branchChangeCallbacks  map[int]func()
	nextCallbackID         int
	availableProviderCount int
	disposed               bool
}

func NewFooterDataProvider(cwd string) *FooterDataProvider {
	return &FooterDataProvider{
		cwd:                   cwd,
		extensionStatuses:     map[string]string{},
		gitPaths:              findFooterGitPaths(cwd),
		branchChangeCallbacks: map[int]func(){},
	}
}

func (f *FooterDataProvider) GetGitBranch() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.branchResolved {
		branch, ok := f.resolveGitBranchLocked()
		f.cachedBranch = branch
		f.branchResolved = true
		return branch, ok
	}
	return f.cachedBranch, f.cachedBranch != ""
}

func (f *FooterDataProvider) GetExtensionStatuses() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneStringMap(f.extensionStatuses)
}

func (f *FooterDataProvider) OnBranchChange(callback func()) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.disposed {
		return func() {}
	}
	id := f.nextCallbackID
	f.nextCallbackID++
	f.branchChangeCallbacks[id] = callback
	return func() {
		f.mu.Lock()
		delete(f.branchChangeCallbacks, id)
		f.mu.Unlock()
	}
}

func (f *FooterDataProvider) SetExtensionStatus(key, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if text == "" {
		delete(f.extensionStatuses, key)
		return
	}
	f.extensionStatuses[key] = text
}

func (f *FooterDataProvider) ClearExtensionStatuses() {
	f.mu.Lock()
	clear(f.extensionStatuses)
	f.mu.Unlock()
}

func (f *FooterDataProvider) GetAvailableProviderCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.availableProviderCount
}

func (f *FooterDataProvider) SetAvailableProviderCount(count int) {
	f.mu.Lock()
	f.availableProviderCount = count
	f.mu.Unlock()
}

func (f *FooterDataProvider) SetCwd(cwd string) {
	f.mu.Lock()
	if f.cwd == cwd {
		f.mu.Unlock()
		return
	}
	f.cwd = cwd
	f.gitPaths = findFooterGitPaths(cwd)
	f.cachedBranch = ""
	f.branchResolved = false
	callbacks := f.callbacksLocked()
	f.mu.Unlock()
	callFooterCallbacks(callbacks)
}

func (f *FooterDataProvider) RefreshGitBranch() {
	f.mu.Lock()
	next, _ := f.resolveGitBranchLocked()
	wasResolved := f.branchResolved
	previous := f.cachedBranch
	f.cachedBranch = next
	f.branchResolved = true
	var callbacks []func()
	if wasResolved && previous != next {
		callbacks = f.callbacksLocked()
	}
	f.mu.Unlock()
	callFooterCallbacks(callbacks)
}

func (f *FooterDataProvider) Dispose() {
	f.mu.Lock()
	f.disposed = true
	f.branchChangeCallbacks = map[int]func(){}
	f.mu.Unlock()
}

func (f *FooterDataProvider) callbacksLocked() []func() {
	callbacks := make([]func(), 0, len(f.branchChangeCallbacks))
	if f.disposed {
		return callbacks
	}
	for _, callback := range f.branchChangeCallbacks {
		callbacks = append(callbacks, callback)
	}
	return callbacks
}

func (f *FooterDataProvider) resolveGitBranchLocked() (string, bool) {
	if f.gitPaths == nil {
		return "", false
	}
	content, err := os.ReadFile(f.gitPaths.headPath)
	if err != nil {
		return "", false
	}
	head := strings.TrimSpace(string(content))
	if strings.HasPrefix(head, "ref: refs/heads/") {
		branch := strings.TrimPrefix(head, "ref: refs/heads/")
		if branch == ".invalid" {
			if resolved := resolveFooterBranchWithGit(f.gitPaths.repoDir); resolved != "" {
				return resolved, true
			}
			return "detached", true
		}
		return branch, true
	}
	return "detached", true
}

func findFooterGitPaths(cwd string) *gitPaths {
	dir := cwd
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil
		}
	}
	dir, _ = filepath.Abs(dir)
	for {
		gitPath := filepath.Join(dir, ".git")
		stat, err := os.Stat(gitPath)
		if err == nil {
			if stat.IsDir() {
				headPath := filepath.Join(gitPath, "HEAD")
				if _, err := os.Stat(headPath); err != nil {
					return nil
				}
				return &gitPaths{repoDir: dir, commonGitDir: gitPath, headPath: headPath}
			}
			content, err := os.ReadFile(gitPath)
			if err != nil {
				return nil
			}
			gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir: ")
			if !ok {
				return nil
			}
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Clean(filepath.Join(dir, gitDir))
			}
			headPath := filepath.Join(gitDir, "HEAD")
			if _, err := os.Stat(headPath); err != nil {
				return nil
			}
			commonGitDir := gitDir
			if commonDirRaw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
				commonGitDir = strings.TrimSpace(string(commonDirRaw))
				if !filepath.IsAbs(commonGitDir) {
					commonGitDir = filepath.Clean(filepath.Join(gitDir, commonGitDir))
				}
			}
			return &gitPaths{repoDir: dir, commonGitDir: commonGitDir, headPath: headPath}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func resolveFooterBranchWithGit(repoDir string) string {
	cmd := exec.Command("git", "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func callFooterCallbacks(callbacks []func()) {
	for _, callback := range callbacks {
		if callback != nil {
			callback()
		}
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

var BuiltInProviderDisplayNames = map[string]string{
	"anthropic":              "Anthropic",
	"amazon-bedrock":         "Amazon Bedrock",
	"azure-openai-responses": "Azure OpenAI Responses",
	"cerebras":               "Cerebras",
	"cloudflare-ai-gateway":  "Cloudflare AI Gateway",
	"cloudflare-workers-ai":  "Cloudflare Workers AI",
	"deepseek":               "DeepSeek",
	"fireworks":              "Fireworks",
	"google":                 "Google Gemini",
	"google-vertex":          "Google Vertex AI",
	"groq":                   "Groq",
	"huggingface":            "Hugging Face",
	"kimi-coding":            "Kimi For Coding",
	"mistral":                "Mistral",
	"minimax":                "MiniMax",
	"minimax-cn":             "MiniMax (China)",
	"moonshotai":             "Moonshot AI",
	"moonshotai-cn":          "Moonshot AI (China)",
	"opencode":               "OpenCode Zen",
	"opencode-go":            "OpenCode Go",
	"openai":                 "OpenAI",
	"openrouter":             "OpenRouter",
	"together":               "Together AI",
	"vercel-ai-gateway":      "Vercel AI Gateway",
	"xai":                    "xAI",
	"zai":                    "ZAI",
	"xiaomi":                 "Xiaomi MiMo",
	"xiaomi-token-plan-cn":   "Xiaomi MiMo Token Plan (China)",
	"xiaomi-token-plan-ams":  "Xiaomi MiMo Token Plan (Amsterdam)",
	"xiaomi-token-plan-sgp":  "Xiaomi MiMo Token Plan (Singapore)",
}

func ProviderDisplayName(provider string) string {
	if name := BuiltInProviderDisplayNames[provider]; name != "" {
		return name
	}
	return provider
}
