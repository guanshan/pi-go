package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
)

const AppName = "pi"

type Mode string

const (
	ModeText Mode = "text"
	ModeJSON Mode = "json"
	ModeRPC  Mode = "rpc"
)

type Diagnostic struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type Args struct {
	Provider           string
	Model              string
	APIKey             string
	SystemPrompt       string
	AppendSystemPrompt []string
	Thinking           ai.ThinkingLevel
	HasThinking        bool
	Continue           bool
	Resume             bool
	Help               bool
	Version            bool
	Mode               Mode
	NoSession          bool
	Session            string
	Fork               string
	SessionDir         string
	Models             []string
	Tools              []string
	ExcludeTools       []string
	NoTools            bool
	NoBuiltinTools     bool
	Name               string
	SessionID          string
	Extensions         []string
	NoExtensions       bool
	Print              bool
	Export             string
	NoSkills           bool
	Skills             []string
	PromptTemplates    []string
	NoPromptTemplates  bool
	Themes             []string
	NoThemes           bool
	NoContextFiles     bool
	ListModels         *string
	Offline            bool
	Verbose            bool
	Messages           []string
	FileArgs           []string
	UnknownFlags       map[string]any
	Diagnostics        []Diagnostic
}

func ParseArgs(argv []string) Args {
	result := Args{
		Mode:         ModeText,
		UnknownFlags: map[string]any{},
	}

	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		next := func() (string, bool) {
			if i+1 >= len(argv) {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Type: "error", Message: fmt.Sprintf("Missing value for %s", arg)})
				return "", false
			}
			i++
			return argv[i], true
		}

		switch {
		case arg == "--help" || arg == "-h":
			result.Help = true
		case arg == "--version" || arg == "-v":
			result.Version = true
		case arg == "--mode":
			if v, ok := next(); ok {
				switch Mode(v) {
				case ModeText, ModeJSON, ModeRPC:
					result.Mode = Mode(v)
				default:
					result.Diagnostics = append(result.Diagnostics, Diagnostic{Type: "error", Message: fmt.Sprintf("Invalid mode %q", v)})
				}
			}
		case arg == "--continue" || arg == "-c":
			result.Continue = true
		case arg == "--resume" || arg == "-r":
			result.Resume = true
		case arg == "--provider":
			if v, ok := next(); ok {
				result.Provider = v
			}
		case arg == "--model":
			if v, ok := next(); ok {
				result.Model = v
			}
		case arg == "--api-key":
			if v, ok := next(); ok {
				result.APIKey = v
			}
		case arg == "--system-prompt":
			if v, ok := next(); ok {
				result.SystemPrompt = v
			}
		case arg == "--append-system-prompt":
			if v, ok := next(); ok {
				result.AppendSystemPrompt = append(result.AppendSystemPrompt, v)
			}
		case arg == "--name" || arg == "-n":
			if i+1 >= len(argv) {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Type: "error", Message: "--name requires a value"})
			} else {
				i++
				result.Name = argv[i]
			}
		case arg == "--no-session":
			result.NoSession = true
		case arg == "--session":
			if v, ok := next(); ok {
				result.Session = v
			}
		case arg == "--session-id":
			if v, ok := next(); ok {
				result.SessionID = v
			}
		case arg == "--fork":
			if v, ok := next(); ok {
				result.Fork = v
			}
		case arg == "--session-dir":
			if v, ok := next(); ok {
				result.SessionDir = v
			}
		case arg == "--models":
			if v, ok := next(); ok {
				result.Models = splitCSV(v)
			}
		case arg == "--no-tools" || arg == "-nt":
			result.NoTools = true
		case arg == "--no-builtin-tools" || arg == "-nbt":
			result.NoBuiltinTools = true
		case arg == "--tools" || arg == "-t":
			if v, ok := next(); ok {
				result.Tools = splitCSV(v)
			}
		case arg == "--exclude-tools" || arg == "-xt":
			if v, ok := next(); ok {
				result.ExcludeTools = splitCSV(v)
			}
		case arg == "--thinking":
			if v, ok := next(); ok {
				if ai.IsValidThinkingLevel(v) {
					result.Thinking = ai.ThinkingLevel(v)
					result.HasThinking = true
				} else {
					result.Diagnostics = append(result.Diagnostics, Diagnostic{Type: "warning", Message: fmt.Sprintf("Invalid thinking level %q. Valid values: off, minimal, low, medium, high, xhigh", v)})
				}
			}
		case arg == "--print" || arg == "-p":
			result.Print = true
			if i+1 < len(argv) {
				n := argv[i+1]
				if !strings.HasPrefix(n, "@") && (!strings.HasPrefix(n, "-") || strings.HasPrefix(n, "---")) {
					result.Messages = append(result.Messages, n)
					i++
				}
			}
		case arg == "--export":
			if v, ok := next(); ok {
				result.Export = v
			}
		case arg == "--extension" || arg == "-e":
			if v, ok := next(); ok {
				result.Extensions = append(result.Extensions, v)
			}
		case arg == "--no-extensions" || arg == "-ne":
			result.NoExtensions = true
		case arg == "--skill":
			if v, ok := next(); ok {
				result.Skills = append(result.Skills, v)
			}
		case arg == "--prompt-template":
			if v, ok := next(); ok {
				result.PromptTemplates = append(result.PromptTemplates, v)
			}
		case arg == "--theme":
			if v, ok := next(); ok {
				result.Themes = append(result.Themes, v)
			}
		case arg == "--no-skills" || arg == "-ns":
			result.NoSkills = true
		case arg == "--no-prompt-templates" || arg == "-np":
			result.NoPromptTemplates = true
		case arg == "--no-themes":
			result.NoThemes = true
		case arg == "--no-context-files" || arg == "-nc":
			result.NoContextFiles = true
		case arg == "--list-models":
			value := ""
			if i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") && !strings.HasPrefix(argv[i+1], "@") {
				i++
				value = argv[i]
			}
			result.ListModels = &value
		case arg == "--verbose":
			result.Verbose = true
		case arg == "--offline":
			result.Offline = true
		case strings.HasPrefix(arg, "@"):
			result.FileArgs = append(result.FileArgs, strings.TrimPrefix(arg, "@"))
		case strings.HasPrefix(arg, "--"):
			nameValue := strings.TrimPrefix(arg, "--")
			if idx := strings.IndexByte(nameValue, '='); idx >= 0 {
				result.UnknownFlags[nameValue[:idx]] = nameValue[idx+1:]
			} else if i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") && !strings.HasPrefix(argv[i+1], "@") {
				i++
				result.UnknownFlags[nameValue] = argv[i]
			} else {
				result.UnknownFlags[nameValue] = true
			}
		case strings.HasPrefix(arg, "-"):
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Type: "error", Message: fmt.Sprintf("Unknown option: %s", arg)})
		default:
			result.Messages = append(result.Messages, arg)
		}
	}

	return result
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func PrintHelp(w io.Writer, extensionFlags []string) {
	fmt.Fprintf(w, `%s - AI coding assistant with read, bash, edit, write tools

Usage:
  %s [options] [@files...] [messages...]

Commands:
  %s install <source> [-l]     Install package source and add to settings
  %s remove <source> [-l]      Remove package source from settings
  %s uninstall <source> [-l]   Alias for remove
  %s update [source|self|pi]   Update pi and installed packages
  %s list                      List installed packages
  %s config                    List and toggle package resources
  %s <command> --help          Show command help

Options:
  --provider <name>              Provider name
  --model <pattern>              Model ID, fuzzy pattern, or provider/model[:thinking]
  --api-key <key>                API key override
  --system-prompt <text>         Replace default system prompt
  --append-system-prompt <text>  Append text or file contents to the system prompt
  --mode <mode>                  Output mode: text, json, or rpc
  --print, -p                    Non-interactive mode
  --continue, -c                 Continue previous session
  --resume, -r                   Select a session to resume
  --session <path|id>            Use specific session file or partial UUID
  --session-id <id>              Create a new session with an explicit id
  --name, -n <name>              Set the session name
  --fork <path|id>               Fork specific session file or partial UUID
  --session-dir <dir>            Directory for session storage and lookup
  --no-session                   Ephemeral mode
  --models <patterns>            Comma-separated model patterns for cycling
  --no-tools, -nt                Disable all tools
  --no-builtin-tools, -nbt       Disable built-in tools by default
  --tools, -t <tools>            Comma-separated allowlist of tool names
  --exclude-tools, -xt <tools>   Comma-separated tool names to exclude
  --thinking <level>             off, minimal, low, medium, high, xhigh
  --extension, -e <path>         Load extension file or package source
  --no-extensions, -ne           Disable extension discovery
  --skill <path>                 Load skill file or directory
  --no-skills, -ns               Disable skills
  --prompt-template <path>       Load prompt template file or directory
  --no-prompt-templates, -np     Disable prompt templates
  --theme <path>                 Load theme file or directory
  --no-themes                    Disable themes
  --no-context-files, -nc        Disable AGENTS.md and CLAUDE.md loading
  --export <file>                Export session file to HTML
  --list-models [search]         List configured models
  --verbose                      Force verbose startup
  --offline                      Disable startup network operations
  --help, -h                     Show help
  --version, -v                  Show version

Built-in tools: read, bash, edit, write, grep, find, ls

Environment Variables:
  ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY, GOOGLE_API_KEY
  AZURE_OPENAI_API_KEY, AZURE_OPENAI_BASE_URL, AZURE_OPENAI_API_VERSION
  DEEPSEEK_API_KEY, GROQ_API_KEY, CEREBRAS_API_KEY, XAI_API_KEY
  FIREWORKS_API_KEY, TOGETHER_API_KEY, OPENROUTER_API_KEY, AI_GATEWAY_API_KEY
  ZAI_API_KEY, MISTRAL_API_KEY, MINIMAX_API_KEY, MOONSHOT_API_KEY
  CLOUDFLARE_API_KEY, CLOUDFLARE_ACCOUNT_ID, CLOUDFLARE_GATEWAY_ID
	PI_AGENT_DIR, PI_SESSION_DIR, PI_CODING_AGENT_DIR, PI_CODING_AGENT_SESSION_DIR, PI_PACKAGE_DIR
  PI_OFFLINE, PI_SKIP_VERSION_CHECK, PI_TELEMETRY, PI_CACHE_RETENTION
  VISUAL, EDITOR
`, AppName, AppName, AppName, AppName, AppName, AppName, AppName, AppName, AppName)
	if len(extensionFlags) > 0 {
		fmt.Fprintln(w, "\nExtension CLI Flags:")
		for _, flag := range extensionFlags {
			fmt.Fprintln(w, "  --"+flag)
		}
	}
}
