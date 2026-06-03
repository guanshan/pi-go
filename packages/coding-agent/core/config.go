package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"github.com/guanshan/pi-go/packages/ai"
	"golang.org/x/text/unicode/norm"
)

const (
	EnvAgentDir              = "PI_AGENT_DIR"
	EnvLegacyAgentDir        = "PI_CODING_AGENT_DIR"
	EnvSessionDir            = "PI_SESSION_DIR"
	EnvLegacySessionDir      = "PI_CODING_AGENT_SESSION_DIR"
	EnvOffline               = "PI_OFFLINE"
	EnvSkipVersionCheck      = "PI_SKIP_VERSION_CHECK"
	EnvStartupBenchmark      = "PI_STARTUP_BENCHMARK"
	DefaultAgentSubDir       = "agent"
	DefaultHTTPIdleTimeoutMS = 300000
)

type Settings struct {
	DefaultProvider         string                  `json:"defaultProvider,omitempty"`
	DefaultModel            string                  `json:"defaultModel,omitempty"`
	DefaultThinkingLevel    ai.ThinkingLevel        `json:"defaultThinkingLevel,omitempty"`
	EnabledModels           []string                `json:"enabledModels,omitempty"`
	SessionDir              string                  `json:"sessionDir,omitempty"`
	Theme                   string                  `json:"theme,omitempty"`
	Transport               string                  `json:"transport,omitempty"`
	SteeringMode            string                  `json:"steeringMode,omitempty"`
	FollowUpMode            string                  `json:"followUpMode,omitempty"`
	AutoCompactionEnabled   *bool                   `json:"autoCompactionEnabled,omitempty"`
	AutoRetryEnabled        *bool                   `json:"autoRetryEnabled,omitempty"`
	Compaction              CompactionConfig        `json:"compaction,omitempty"`
	BranchSummary           BranchSummary           `json:"branchSummary,omitempty"`
	Retry                   RetryConfig             `json:"retry,omitempty"`
	Terminal                TerminalConfig          `json:"terminal,omitempty"`
	Images                  ImageSettings           `json:"images,omitempty"`
	ImageAutoResize         *bool                   `json:"imageAutoResize,omitempty"`
	BlockImages             *bool                   `json:"blockImages,omitempty"`
	HideThinkingBlock       *bool                   `json:"hideThinkingBlock,omitempty"`
	QuietStartup            *bool                   `json:"quietStartup,omitempty"`
	CollapseChangelog       *bool                   `json:"collapseChangelog,omitempty"`
	LastChangelogVersion    string                  `json:"lastChangelogVersion,omitempty"`
	EnableInstallTelemetry  *bool                   `json:"enableInstallTelemetry,omitempty"`
	HTTPIdleTimeoutMS       *HTTPIdleTimeoutSetting `json:"httpIdleTimeoutMs,omitempty"`
	ShellPath               string                  `json:"shellPath,omitempty"`
	ShellCommandPrefix      string                  `json:"shellCommandPrefix,omitempty"`
	BashCommandPrefix       string                  `json:"bashCommandPrefix,omitempty"`
	NPMCommand              []string                `json:"npmCommand,omitempty"`
	Packages                []PackageSetting        `json:"packages,omitempty"`
	InstalledPackages       []PackageRecord         `json:"installedPackages,omitempty"`
	Extensions              []string                `json:"extensions,omitempty"`
	Skills                  []string                `json:"skills,omitempty"`
	Prompts                 []string                `json:"prompts,omitempty"`
	Themes                  []string                `json:"themes,omitempty"`
	DisabledExtensions      []string                `json:"disabledExtensions,omitempty"`
	DisabledSkills          []string                `json:"disabledSkills,omitempty"`
	DisabledPromptTemplates []string                `json:"disabledPromptTemplates,omitempty"`
	DisabledThemes          []string                `json:"disabledThemes,omitempty"`
	EnableSkillCommands     *bool                   `json:"enableSkillCommands,omitempty"`
	ThinkingBudgets         ThinkingBudgets         `json:"thinkingBudgets,omitempty"`
	DoubleEscapeAction      string                  `json:"doubleEscapeAction,omitempty"`
	TreeFilterMode          string                  `json:"treeFilterMode,omitempty"`
	EditorPaddingX          *int                    `json:"editorPaddingX,omitempty"`
	AutocompleteMaxVisible  *int                    `json:"autocompleteMaxVisible,omitempty"`
	ShowHardwareCursor      *bool                   `json:"showHardwareCursor,omitempty"`
	Markdown                MarkdownConfig          `json:"markdown,omitempty"`
	Warnings                WarningConfig           `json:"warnings,omitempty"`
	Raw                     json.RawMessage         `json:"-"`
}

func (s *Settings) UnmarshalJSON(data []byte) error {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	migrateLegacySettings(fields)
	normalized, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	type settingsAlias Settings
	var out settingsAlias
	if err := json.Unmarshal(normalized, &out); err != nil {
		return err
	}
	out.Raw = append([]byte(nil), data...)
	*s = Settings(out)
	return nil
}

func migrateLegacySettings(fields map[string]json.RawMessage) {
	if len(fields) == 0 {
		return
	}
	if _, has := fields["steeringMode"]; !has {
		if queueMode, ok := fields["queueMode"]; ok {
			fields["steeringMode"] = queueMode
		}
	}
	delete(fields, "queueMode")

	if _, has := fields["transport"]; !has {
		if raw, ok := fields["websockets"]; ok {
			var enabled bool
			if err := json.Unmarshal(raw, &enabled); err == nil {
				if enabled {
					fields["transport"] = json.RawMessage(`"websocket"`)
				} else {
					fields["transport"] = json.RawMessage(`"sse"`)
				}
			}
		}
	}
	delete(fields, "websockets")

	if raw, ok := fields["skills"]; ok {
		var skillObject struct {
			EnableSkillCommands *bool    `json:"enableSkillCommands"`
			CustomDirectories   []string `json:"customDirectories"`
		}
		if err := json.Unmarshal(raw, &skillObject); err == nil {
			if skillObject.EnableSkillCommands != nil {
				if _, exists := fields["enableSkillCommands"]; !exists {
					if encoded, err := json.Marshal(*skillObject.EnableSkillCommands); err == nil {
						fields["enableSkillCommands"] = encoded
					}
				}
			}
			if len(skillObject.CustomDirectories) > 0 {
				if encoded, err := json.Marshal(skillObject.CustomDirectories); err == nil {
					fields["skills"] = encoded
				}
			} else {
				delete(fields, "skills")
			}
		}
	}

	if raw, ok := fields["retry"]; ok {
		retry := map[string]json.RawMessage{}
		if err := json.Unmarshal(raw, &retry); err == nil {
			if maxDelay, ok := retry["maxDelayMs"]; ok {
				provider := map[string]json.RawMessage{}
				if existing, ok := retry["provider"]; ok && string(existing) != "null" {
					_ = json.Unmarshal(existing, &provider)
				}
				if _, exists := provider["maxRetryDelayMs"]; !exists || string(provider["maxRetryDelayMs"]) == "null" {
					provider["maxRetryDelayMs"] = maxDelay
				}
				if encoded, err := json.Marshal(provider); err == nil {
					retry["provider"] = encoded
				}
				delete(retry, "maxDelayMs")
				if encoded, err := json.Marshal(retry); err == nil {
					fields["retry"] = encoded
				}
			}
		}
	}
}

type CompactionConfig struct {
	Enabled          *bool `json:"enabled,omitempty"`
	ReserveTokens    int   `json:"reserveTokens,omitempty"`
	KeepRecentTokens int   `json:"keepRecentTokens,omitempty"`
}

type BranchSummary struct {
	ReserveTokens int   `json:"reserveTokens,omitempty"`
	SkipPrompt    *bool `json:"skipPrompt,omitempty"`
}

type RetryConfig struct {
	Enabled     *bool               `json:"enabled,omitempty"`
	MaxRetries  int                 `json:"maxRetries,omitempty"`
	BaseDelayMS int                 `json:"baseDelayMs,omitempty"`
	Provider    ProviderRetryConfig `json:"provider,omitempty"`
}

type ProviderRetryConfig struct {
	TimeoutMS       int `json:"timeoutMs,omitempty"`
	MaxRetries      int `json:"maxRetries,omitempty"`
	MaxRetryDelayMS int `json:"maxRetryDelayMs,omitempty"`
}

type TerminalConfig struct {
	ShowImages           *bool `json:"showImages,omitempty"`
	ImageWidthCells      int   `json:"imageWidthCells,omitempty"`
	ClearOnShrink        *bool `json:"clearOnShrink,omitempty"`
	ShowTerminalProgress *bool `json:"showTerminalProgress,omitempty"`
}

type ImageSettings struct {
	AutoResize  *bool `json:"autoResize,omitempty"`
	BlockImages *bool `json:"blockImages,omitempty"`
}

type ThinkingBudgets struct {
	Minimal int `json:"minimal,omitempty"`
	Low     int `json:"low,omitempty"`
	Medium  int `json:"medium,omitempty"`
	High    int `json:"high,omitempty"`
}

type MarkdownConfig struct {
	CodeBlockIndent string `json:"codeBlockIndent,omitempty"`
}

type WarningConfig struct {
	AnthropicExtraUsage *bool `json:"anthropicExtraUsage,omitempty"`
}

type HTTPIdleTimeoutSetting int

func (h *HTTPIdleTimeoutSetting) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return nil
		}
		if strings.EqualFold(trimmed, "disabled") {
			*h = 0
			return nil
		}
		value, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return fmt.Errorf("invalid httpIdleTimeoutMs setting: %s", text)
		}
		return h.setFloat(value)
	}
	var value float64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return h.setFloat(value)
}

func (h *HTTPIdleTimeoutSetting) setFloat(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return fmt.Errorf("invalid httpIdleTimeoutMs setting: %v", value)
	}
	*h = HTTPIdleTimeoutSetting(math.Floor(value))
	return nil
}

func (h HTTPIdleTimeoutSetting) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(h))
}

type PackageRecord struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Local  bool   `json:"local,omitempty"`
	Pinned bool   `json:"pinned,omitempty"`
}

type PackageSetting struct {
	Source     string   `json:"source"`
	Extensions []string `json:"extensions,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Prompts    []string `json:"prompts,omitempty"`
	Themes     []string `json:"themes,omitempty"`
}

func (p *PackageSetting) UnmarshalJSON(data []byte) error {
	var source string
	if err := json.Unmarshal(data, &source); err == nil {
		p.Source = source
		return nil
	}
	type packageSetting PackageSetting
	var object packageSetting
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	*p = PackageSetting(object)
	return nil
}

func (p PackageSetting) MarshalJSON() ([]byte, error) {
	if p.Extensions == nil && p.Skills == nil && p.Prompts == nil && p.Themes == nil {
		return json.Marshal(p.Source)
	}
	type packageSetting PackageSetting
	return json.Marshal(packageSetting(p))
}

type SettingsManager struct {
	CWD      string
	AgentDir string
	Global   Settings
	Project  Settings
	Errors   []error
}

func HomeDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	if runtime.GOOS == "windows" {
		return os.Getenv("USERPROFILE")
	}
	return os.Getenv("HOME")
}

func ExpandTilde(path string) string {
	if path == "~" {
		return HomeDir()
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(HomeDir(), path[2:])
	}
	return path
}

func ExpandTildePath(path string) string {
	return ExpandTilde(path)
}

func AbsPath(path string) (string, error) {
	path = ExpandTilde(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func ResolveInCWD(cwd, path string) string {
	path = normalizePathInput(path, false)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

var macOSScreenshotAMPMPattern = regexp.MustCompile(` (?i:am|pm)\.`)

func ResolveReadPath(cwd, path string) string {
	resolved := ResolveInCWD(cwd, normalizePathInput(path, true))
	if fileExists(resolved) {
		return resolved
	}
	for _, candidate := range readPathVariants(resolved) {
		if candidate != resolved && fileExists(candidate) {
			return candidate
		}
	}
	return resolved
}

func readPathVariants(path string) []string {
	amPM := macOSScreenshotAMPMPattern.ReplaceAllStringFunc(path, func(match string) string {
		return "\u202f" + match[1:]
	})
	nfd := norm.NFD.String(path)
	curly := strings.ReplaceAll(path, "'", "\u2019")
	return []string{
		amPM,
		nfd,
		curly,
		strings.ReplaceAll(nfd, "'", "\u2019"),
	}
}

func normalizePathInput(path string, stripAtPrefix bool) string {
	if stripAtPrefix && strings.HasPrefix(path, "@") {
		path = strings.TrimPrefix(path, "@")
	}
	path = strings.Map(func(r rune) rune {
		switch {
		case r == '\u00a0' || r == '\u202f' || r == '\u205f' || r == '\u3000':
			return ' '
		case r >= '\u2000' && r <= '\u200a':
			return ' '
		default:
			return r
		}
	}, path)
	if strings.HasPrefix(path, "file://") {
		if u, err := url.Parse(path); err == nil {
			if decoded, err := url.PathUnescape(u.Path); err == nil && decoded != "" {
				path = decoded
			}
		}
	}
	path = ExpandTilde(path)
	return strings.TrimFunc(path, unicode.IsControl)
}

func AgentDir() string {
	if v := os.Getenv(EnvAgentDir); v != "" {
		return filepath.Clean(ExpandTilde(v))
	}
	if v := os.Getenv(EnvLegacyAgentDir); v != "" {
		return filepath.Clean(ExpandTilde(v))
	}
	return filepath.Join(HomeDir(), ConfigDirName, DefaultAgentSubDir)
}

// BinDir returns the agent bin directory (<agentDir>/bin), where migrated and
// package-installed executables (fd, rg, CLIs) live. Mirrors getBinDir() in
// src/config.ts:519-520. The bash tool prepends this to PATH for every command.
func BinDir() string {
	return filepath.Join(AgentDir(), "bin")
}

func GetPackageDir() string {
	// PI_PACKAGE_DIR override mirrors TS getPackageDir (config.ts:345-348), useful
	// where store paths tokenize poorly (Nix/Guix) or for tests.
	if envDir := os.Getenv("PI_PACKAGE_DIR"); envDir != "" {
		return filepath.Clean(envDir)
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return ""
	}
	return filepath.Dir(exe)
}

// ReadmePath, DocsPath, and ExamplesPath return absolute paths to the shipped
// README.md, docs directory, and examples directory respectively, mirroring TS
// getReadmePath/getDocsPath/getExamplesPath (config.ts:403-415). They are
// referenced by the default system prompt's "Pi documentation" section.
func ReadmePath() string   { return resolvePackagePath("README.md") }
func DocsPath() string     { return resolvePackagePath("docs") }
func ExamplesPath() string { return resolvePackagePath("examples") }

func resolvePackagePath(rel string) string {
	p := filepath.Join(GetPackageDir(), rel)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func ProjectPiDir(cwd string) string {
	return filepath.Join(cwd, ConfigDirName)
}

func NewSettingsManager(cwd, agentDir string) *SettingsManager {
	sm := &SettingsManager{CWD: cwd, AgentDir: agentDir}
	sm.Global, _ = sm.load(filepath.Join(agentDir, "settings.json"))
	sm.Project, _ = sm.load(filepath.Join(ProjectPiDir(cwd), "settings.json"))
	return sm
}

func (s *SettingsManager) load(path string) (Settings, bool) {
	var settings Settings
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, false
	}
	if err != nil {
		s.Errors = append(s.Errors, err)
		return settings, false
	}
	settings.Raw = append([]byte(nil), data...)
	if err := json.Unmarshal(data, &settings); err != nil {
		s.Errors = append(s.Errors, err)
		return settings, false
	}
	return settings, true
}

func (s *SettingsManager) SaveGlobal() error {
	return writeJSON(filepath.Join(s.AgentDir, "settings.json"), s.Global)
}

func (s *SettingsManager) SaveProject() error {
	return writeJSON(filepath.Join(ProjectPiDir(s.CWD), "settings.json"), s.Project)
}

func (s *SettingsManager) mergedString(global, project, fallback string) string {
	if project != "" {
		return project
	}
	if global != "" {
		return global
	}
	return fallback
}

func (s *SettingsManager) DefaultProvider() string {
	return s.mergedString(s.Global.DefaultProvider, s.Project.DefaultProvider, "")
}

func (s *SettingsManager) DefaultModel() string {
	return s.mergedString(s.Global.DefaultModel, s.Project.DefaultModel, "")
}

func (s *SettingsManager) DefaultThinkingLevel() ai.ThinkingLevel {
	level := s.Project.DefaultThinkingLevel
	if level == "" {
		level = s.Global.DefaultThinkingLevel
	}
	if level == "" || !ai.IsValidThinkingLevel(string(level)) {
		return ai.ThinkingMedium
	}
	return level
}

func (s *SettingsManager) EnabledModels() []string {
	if len(s.Project.EnabledModels) > 0 {
		return append([]string(nil), s.Project.EnabledModels...)
	}
	return append([]string(nil), s.Global.EnabledModels...)
}

// SetDefaultModelAndProvider persists the selected model+provider as the global
// default so the next launch (new session) remembers the choice. Mirrors
// settings-manager.ts:617 setDefaultModelAndProvider.
func (s *SettingsManager) SetDefaultModelAndProvider(provider, modelID string) error {
	s.Global.DefaultProvider = provider
	s.Global.DefaultModel = modelID
	return s.SaveGlobal()
}

// SetDefaultThinkingLevel persists the thinking level as the global default.
// Mirrors settings-manager.ts:659 setDefaultThinkingLevel.
func (s *SettingsManager) SetDefaultThinkingLevel(level ai.ThinkingLevel) error {
	s.Global.DefaultThinkingLevel = level
	return s.SaveGlobal()
}

func (s *SettingsManager) SessionDir() string {
	if s.Project.SessionDir != "" {
		return ExpandTilde(s.Project.SessionDir)
	}
	if s.Global.SessionDir != "" {
		return ExpandTilde(s.Global.SessionDir)
	}
	if v := os.Getenv(EnvSessionDir); v != "" {
		return ExpandTilde(v)
	}
	if v := os.Getenv(EnvLegacySessionDir); v != "" {
		return ExpandTilde(v)
	}
	return filepath.Join(s.AgentDir, "sessions")
}

func (s *SettingsManager) Bool(project *bool, global *bool, fallback bool) bool {
	if project != nil {
		return *project
	}
	if global != nil {
		return *global
	}
	return fallback
}

func (s *SettingsManager) AutoCompactionEnabled() bool {
	return s.Bool(firstBool(s.Project.Compaction.Enabled, s.Project.AutoCompactionEnabled), firstBool(s.Global.Compaction.Enabled, s.Global.AutoCompactionEnabled), true)
}

func (s *SettingsManager) AutoRetryEnabled() bool {
	return s.Bool(firstBool(s.Project.Retry.Enabled, s.Project.AutoRetryEnabled), firstBool(s.Global.Retry.Enabled, s.Global.AutoRetryEnabled), true)
}

func (s *SettingsManager) Transport() string {
	return s.mergedString(s.Global.Transport, s.Project.Transport, "auto")
}

// ThinkingBudgets mirrors TS SettingsManager.getThinkingBudgets()
// (settings-manager.ts:926-928): the custom per-level token budgets, with the
// project file deep-merged over the global one (deepMergeSettings(global,
// project)). Returns nil when neither file sets any budget so the provider can
// fall back to its built-in defaults.
func (s *SettingsManager) ThinkingBudgets() *ai.ThinkingBudgets {
	merged := ai.ThinkingBudgets{
		Minimal: firstPositiveInt(s.Project.ThinkingBudgets.Minimal, s.Global.ThinkingBudgets.Minimal, 0),
		Low:     firstPositiveInt(s.Project.ThinkingBudgets.Low, s.Global.ThinkingBudgets.Low, 0),
		Medium:  firstPositiveInt(s.Project.ThinkingBudgets.Medium, s.Global.ThinkingBudgets.Medium, 0),
		High:    firstPositiveInt(s.Project.ThinkingBudgets.High, s.Global.ThinkingBudgets.High, 0),
	}
	if merged == (ai.ThinkingBudgets{}) {
		return nil
	}
	return &merged
}

func (s *SettingsManager) CompactionReserveTokens() int {
	return firstPositiveInt(s.Project.Compaction.ReserveTokens, s.Global.Compaction.ReserveTokens, 16384)
}

func (s *SettingsManager) CompactionKeepRecentTokens() int {
	return firstPositiveInt(s.Project.Compaction.KeepRecentTokens, s.Global.Compaction.KeepRecentTokens, 20000)
}

func (s *SettingsManager) BranchSummaryReserveTokens() int {
	return firstPositiveInt(s.Project.BranchSummary.ReserveTokens, s.Global.BranchSummary.ReserveTokens, 16384)
}

func (s *SettingsManager) BranchSummarySkipPrompt() bool {
	return s.Bool(s.Project.BranchSummary.SkipPrompt, s.Global.BranchSummary.SkipPrompt, false)
}

func (s *SettingsManager) RetryMaxRetries() int {
	return firstPositiveInt(s.Project.Retry.MaxRetries, s.Global.Retry.MaxRetries, 3)
}

func (s *SettingsManager) RetryBaseDelayMS() int {
	return firstPositiveInt(s.Project.Retry.BaseDelayMS, s.Global.Retry.BaseDelayMS, 2000)
}

func (s *SettingsManager) ProviderRetryTimeoutMS() int {
	return firstPositiveInt(s.Project.Retry.Provider.TimeoutMS, s.Global.Retry.Provider.TimeoutMS, 0)
}

func (s *SettingsManager) ProviderRetryMaxRetries() int {
	return firstPositiveInt(s.Project.Retry.Provider.MaxRetries, s.Global.Retry.Provider.MaxRetries, 0)
}

func (s *SettingsManager) ProviderRetryMaxDelayMS() int {
	return firstPositiveInt(s.Project.Retry.Provider.MaxRetryDelayMS, s.Global.Retry.Provider.MaxRetryDelayMS, 60000)
}

func (s *SettingsManager) HideThinkingBlock() bool {
	return s.Bool(s.Project.HideThinkingBlock, s.Global.HideThinkingBlock, false)
}

func (s *SettingsManager) QuietStartup() bool {
	return s.Bool(s.Project.QuietStartup, s.Global.QuietStartup, false)
}

func (s *SettingsManager) CollapseChangelog() bool {
	return s.Bool(s.Project.CollapseChangelog, s.Global.CollapseChangelog, false)
}

// LastChangelogVersion returns the most recent version for which the post-upgrade
// changelog was shown. Mirrors getLastChangelogVersion() in settings-manager.ts:
// read from the merged view (project overrides global), empty if never recorded.
func (s *SettingsManager) LastChangelogVersion() string {
	if s.Project.LastChangelogVersion != "" {
		return s.Project.LastChangelogVersion
	}
	return s.Global.LastChangelogVersion
}

// SetLastChangelogVersion records the version whose changelog has been shown.
// Mirrors setLastChangelogVersion() in settings-manager.ts, which writes to the
// global settings. The caller persists via SaveGlobal.
func (s *SettingsManager) SetLastChangelogVersion(version string) {
	s.Global.LastChangelogVersion = version
}

func (s *SettingsManager) EnableInstallTelemetry() bool {
	return s.Bool(s.Project.EnableInstallTelemetry, s.Global.EnableInstallTelemetry, true)
}

func (s *SettingsManager) NPMCommand() []string {
	if len(s.Project.NPMCommand) > 0 {
		return append([]string(nil), s.Project.NPMCommand...)
	}
	return append([]string(nil), s.Global.NPMCommand...)
}

func (s *SettingsManager) HTTPIdleTimeoutMS() int {
	if s.Project.HTTPIdleTimeoutMS != nil {
		return int(*s.Project.HTTPIdleTimeoutMS)
	}
	if s.Global.HTTPIdleTimeoutMS != nil {
		return int(*s.Global.HTTPIdleTimeoutMS)
	}
	return DefaultHTTPIdleTimeoutMS
}

func (s *SettingsManager) BlockImages() bool {
	return s.Bool(firstBool(s.Project.Images.BlockImages, s.Project.BlockImages), firstBool(s.Global.Images.BlockImages, s.Global.BlockImages), false)
}

func (s *SettingsManager) ImageAutoResize() bool {
	return s.Bool(firstBool(s.Project.Images.AutoResize, s.Project.ImageAutoResize), firstBool(s.Global.Images.AutoResize, s.Global.ImageAutoResize), true)
}

func (s *SettingsManager) ShowImages() bool {
	return s.Bool(s.Project.Terminal.ShowImages, s.Global.Terminal.ShowImages, true)
}

func (s *SettingsManager) ImageWidthCells() int {
	return firstPositiveInt(s.Project.Terminal.ImageWidthCells, s.Global.Terminal.ImageWidthCells, 60)
}

func (s *SettingsManager) ClearOnShrink() bool {
	if value := firstBool(s.Project.Terminal.ClearOnShrink, s.Global.Terminal.ClearOnShrink); value != nil {
		return *value
	}
	return os.Getenv("PI_CLEAR_ON_SHRINK") == "1"
}

func (s *SettingsManager) ShowTerminalProgress() bool {
	return s.Bool(s.Project.Terminal.ShowTerminalProgress, s.Global.Terminal.ShowTerminalProgress, false)
}

func (s *SettingsManager) ShellCommandPrefix() string {
	project := firstString(s.Project.ShellCommandPrefix, s.Project.BashCommandPrefix)
	global := firstString(s.Global.ShellCommandPrefix, s.Global.BashCommandPrefix)
	return s.mergedString(global, project, "")
}

func (s *SettingsManager) SteeringMode() string {
	return s.mergedString(s.Global.SteeringMode, s.Project.SteeringMode, "one-at-a-time")
}

func (s *SettingsManager) FollowUpMode() string {
	return s.mergedString(s.Global.FollowUpMode, s.Project.FollowUpMode, "one-at-a-time")
}

func (s *SettingsManager) EnableSkillCommands() bool {
	return s.Bool(s.Project.EnableSkillCommands, s.Global.EnableSkillCommands, true)
}

func (s *SettingsManager) DoubleEscapeAction() string {
	value := s.mergedString(s.Global.DoubleEscapeAction, s.Project.DoubleEscapeAction, "tree")
	switch value {
	case "fork", "tree", "none":
		return value
	default:
		return "tree"
	}
}

func (s *SettingsManager) TreeFilterMode() string {
	value := s.mergedString(s.Global.TreeFilterMode, s.Project.TreeFilterMode, "default")
	switch value {
	case "default", "no-tools", "user-only", "labeled-only", "all":
		return value
	default:
		return "default"
	}
}

func (s *SettingsManager) EditorPaddingX() int {
	return clampInt(firstIntPtr(s.Project.EditorPaddingX, s.Global.EditorPaddingX, 0), 0, 3)
}

func (s *SettingsManager) AutocompleteMaxVisible() int {
	return clampInt(firstIntPtr(s.Project.AutocompleteMaxVisible, s.Global.AutocompleteMaxVisible, 5), 3, 20)
}

func (s *SettingsManager) ShowHardwareCursor() bool {
	if value := firstBool(s.Project.ShowHardwareCursor, s.Global.ShowHardwareCursor); value != nil {
		return *value
	}
	return os.Getenv("PI_HARDWARE_CURSOR") == "1"
}

func (s *SettingsManager) CodeBlockIndent() string {
	return s.mergedString(s.Global.Markdown.CodeBlockIndent, s.Project.Markdown.CodeBlockIndent, "  ")
}

func (s *SettingsManager) AnthropicExtraUsageWarning() bool {
	return s.Bool(s.Project.Warnings.AnthropicExtraUsage, s.Global.Warnings.AnthropicExtraUsage, true)
}

func (s *SettingsManager) InstalledPackages(local bool) []PackageRecord {
	if local {
		return append([]PackageRecord(nil), s.Project.InstalledPackages...)
	}
	return append([]PackageRecord(nil), s.Global.InstalledPackages...)
}

func (s *SettingsManager) PackageSources(local bool) []PackageSetting {
	if local {
		return append([]PackageSetting(nil), s.Project.Packages...)
	}
	return append([]PackageSetting(nil), s.Global.Packages...)
}

func firstBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstPositiveInt(project, global, fallback int) int {
	if project > 0 {
		return project
	}
	if global > 0 {
		return global
	}
	return fallback
}

func firstIntPtr(project, global *int, fallback int) int {
	if project != nil {
		return *project
	}
	if global != nil {
		return *global
	}
	return fallback
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *SettingsManager) AddPackage(record PackageRecord) {
	target := &s.Global.InstalledPackages
	if record.Local {
		target = &s.Project.InstalledPackages
	}
	for i := range *target {
		if (*target)[i].Source == record.Source {
			(*target)[i] = record
			return
		}
	}
	*target = append(*target, record)
}

func (s *SettingsManager) AddPackageSource(source string, local bool) bool {
	source = strings.TrimSpace(source)
	if source == "" {
		return false
	}
	target := &s.Global.Packages
	if local {
		target = &s.Project.Packages
	}
	for _, record := range *target {
		if record.Source == source {
			return false
		}
	}
	*target = append(*target, PackageSetting{Source: source})
	return true
}

func (s *SettingsManager) RemovePackage(source string, local bool) bool {
	target := &s.Global.InstalledPackages
	if local {
		target = &s.Project.InstalledPackages
	}
	for i, record := range *target {
		if record.Source == source {
			*target = append((*target)[:i], (*target)[i+1:]...)
			return true
		}
	}
	return false
}

func (s *SettingsManager) RemovePackageSource(source string, local bool) bool {
	target := &s.Global.Packages
	if local {
		target = &s.Project.Packages
	}
	for i, record := range *target {
		if record.Source == source {
			*target = append((*target)[:i], (*target)[i+1:]...)
			return true
		}
	}
	return false
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// TS writes settings.json with 2-space indent (settings-manager.ts:320,536).
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
