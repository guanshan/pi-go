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
		// next returns the following argument as this flag's value. When a known
		// long value-flag is the last argument (no value follows), TS args.ts gates
		// each known flag on `i + 1 < args.length`; failing that guard the flag
		// falls through to the unknown-flag branch and is recorded as a boolean true
		// (NOT an error). Short aliases do not have a matching unknown-flag branch,
		// so missing-value aliases are reported as Unknown option below.
		next := func() (string, bool) {
			if i+1 >= len(argv) {
				result.UnknownFlags[strings.TrimLeft(arg, "-")] = true
				return "", false
			}
			i++
			return argv[i], true
		}
		nextShort := func() (string, bool) {
			if i+1 >= len(argv) {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Type: "error", Message: fmt.Sprintf("Unknown option: %s", arg)})
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
		case arg == "--tools":
			if v, ok := next(); ok {
				result.Tools = splitCSV(v)
			}
		case arg == "-t":
			if v, ok := nextShort(); ok {
				result.Tools = splitCSV(v)
			}
		case arg == "--exclude-tools":
			if v, ok := next(); ok {
				result.ExcludeTools = splitCSV(v)
			}
		case arg == "-xt":
			if v, ok := nextShort(); ok {
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
		case arg == "--extension":
			if v, ok := next(); ok {
				result.Extensions = append(result.Extensions, v)
			}
		case arg == "-e":
			if v, ok := nextShort(); ok {
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
	app := AppName
	fmt.Fprintf(w, `%s - AI coding assistant with read, bash, edit, write tools

Usage:
  %s [options] [@files...] [messages...]

Commands:
  %s install <source> [-l]     Install extension source and add to settings
  %s remove <source> [-l]      Remove extension source from settings
  %s uninstall <source> [-l]   Alias for remove
  %s update [source|self|pi]   Update pi and installed extensions
  %s list                      List installed extensions from settings
  %s config                    Open TUI to enable/disable package resources
  %s <command> --help          Show help for install/remove/uninstall/update/list

Options:
  --provider <name>              Provider name (default: google)
  --model <pattern>              Model pattern or ID (supports "provider/id" and optional ":<thinking>")
  --api-key <key>                API key (defaults to env vars)
  --system-prompt <text>         System prompt (default: coding assistant prompt)
  --append-system-prompt <text>  Append text or file contents to the system prompt (can be used multiple times)
  --mode <mode>                  Output mode: text (default), json, or rpc
  --print, -p                    Non-interactive mode: process prompt and exit
  --continue, -c                 Continue previous session
  --resume, -r                   Select a session to resume
  --session <path|id>            Use specific session file or partial UUID
  --session-id <id>              Use exact project session ID, creating it if missing
  --fork <path|id>               Fork specific session file or partial UUID into a new session
  --session-dir <dir>            Directory for session storage and lookup
  --no-session                   Don't save session (ephemeral)
  --name, -n <name>              Set session display name
  --models <patterns>            Comma-separated model patterns for Ctrl+P cycling
                                 Supports globs (anthropic/*, *sonnet*) and fuzzy matching
  --no-tools, -nt                Disable all tools by default (built-in and extension)
  --no-builtin-tools, -nbt       Disable built-in tools by default but keep extension/custom tools enabled
  --tools, -t <tools>            Comma-separated allowlist of tool names to enable
                                 Applies to built-in, extension, and custom tools
  --exclude-tools, -xt <tools>   Comma-separated denylist of tool names to disable
                                 Applies to built-in, extension, and custom tools
  --thinking <level>             Set thinking level: off, minimal, low, medium, high, xhigh
  --extension, -e <path>         Load an extension file (can be used multiple times)
  --no-extensions, -ne           Disable extension discovery (explicit -e paths still work)
  --skill <path>                 Load a skill file or directory (can be used multiple times)
  --no-skills, -ns               Disable skills discovery and loading
  --prompt-template <path>       Load a prompt template file or directory (can be used multiple times)
  --no-prompt-templates, -np     Disable prompt template discovery and loading
  --theme <path>                 Load a theme file or directory (can be used multiple times)
  --no-themes                    Disable theme discovery and loading
  --no-context-files, -nc        Disable AGENTS.md and CLAUDE.md discovery and loading
  --export <file>                Export session file to HTML and exit
  --list-models [search]         List available models (with optional fuzzy search)
  --verbose                      Force verbose startup (overrides quietStartup setting)
  --offline                      Disable startup network operations (same as PI_OFFLINE=1)
  --help, -h                     Show this help
  --version, -v                  Show version number

Extensions can register additional flags (e.g., --plan from plan-mode extension).
`, app, app, app, app, app, app, app, app, app)
	if len(extensionFlags) > 0 {
		fmt.Fprintln(w, "Extension CLI Flags:")
		for _, flag := range extensionFlags {
			fmt.Fprintln(w, "  --"+flag)
		}
	}
	fmt.Fprintf(w, `
Examples:
  # Interactive mode
  %[1]s

  # Interactive mode with initial prompt
  %[1]s "List all .ts files in src/"

  # Include files in initial message
  %[1]s @prompt.md @image.png "What color is the sky?"

  # Non-interactive mode (process and exit)
  %[1]s -p "List all .ts files in src/"

  # Multiple messages (interactive)
  %[1]s "Read package.json" "What dependencies do we have?"

  # Continue previous session
  %[1]s --continue "What did we discuss?"

  # Start a named session
  %[1]s --name "Refactor auth module"

  # Use different model
  %[1]s --provider openai --model gpt-4o-mini "Help me refactor this code"

  # Use model with provider prefix (no --provider needed)
  %[1]s --model openai/gpt-4o "Help me refactor this code"

  # Use model with thinking level shorthand
  %[1]s --model sonnet:high "Solve this complex problem"

  # Limit model cycling to specific models
  %[1]s --models claude-sonnet,claude-haiku,gpt-4o

  # Limit to a specific provider with glob pattern
  %[1]s --models "github-copilot/*"

  # Cycle models with fixed thinking levels
  %[1]s --models sonnet:high,haiku:low

  # Start with a specific thinking level
  %[1]s --thinking high "Solve this complex problem"

  # Read-only mode (no file modifications possible)
  %[1]s --tools read,grep,find,ls -p "Review the code in src/"

  # Disable one tool while keeping the rest available
  %[1]s --exclude-tools ask_question

  # Export a session file to HTML
  %[1]s --export ~/.pi/agent/sessions/--path--/session.jsonl
  %[1]s --export session.jsonl output.html

Environment Variables:
  ANTHROPIC_API_KEY                - Anthropic Claude API key
  ANTHROPIC_OAUTH_TOKEN            - Anthropic OAuth token (alternative to API key)
  OPENAI_API_KEY                   - OpenAI GPT API key
  AZURE_OPENAI_API_KEY             - Azure OpenAI API key
  AZURE_OPENAI_BASE_URL            - Azure OpenAI/Cognitive Services base URL (e.g. https://{resource}.openai.azure.com)
  AZURE_OPENAI_RESOURCE_NAME       - Azure OpenAI resource name (alternative to base URL)
  AZURE_OPENAI_API_VERSION         - Azure OpenAI API version (default: v1)
  AZURE_OPENAI_DEPLOYMENT_NAME_MAP - Azure OpenAI model=deployment map (comma-separated)
  DEEPSEEK_API_KEY                 - DeepSeek API key
  GEMINI_API_KEY                   - Google Gemini API key
  GROQ_API_KEY                     - Groq API key
  CEREBRAS_API_KEY                 - Cerebras API key
  XAI_API_KEY                      - xAI Grok API key
  FIREWORKS_API_KEY                - Fireworks API key
  TOGETHER_API_KEY                 - Together AI API key
  OPENROUTER_API_KEY               - OpenRouter API key
  AI_GATEWAY_API_KEY               - Vercel AI Gateway API key
  ZAI_API_KEY                      - ZAI API key
  MISTRAL_API_KEY                  - Mistral API key
  MINIMAX_API_KEY                  - MiniMax API key
  MOONSHOT_API_KEY                 - Moonshot AI API key
  OPENCODE_API_KEY                 - OpenCode Zen/OpenCode Go API key
  KIMI_API_KEY                     - Kimi For Coding API key
  CLOUDFLARE_API_KEY               - Cloudflare API token (Workers AI and AI Gateway)
  CLOUDFLARE_ACCOUNT_ID            - Cloudflare account id (required for both)
  CLOUDFLARE_GATEWAY_ID            - Cloudflare AI Gateway slug (required for AI Gateway)
  XIAOMI_API_KEY                   - Xiaomi MiMo API key (api.xiaomimimo.com billing)
  XIAOMI_TOKEN_PLAN_CN_API_KEY     - Xiaomi MiMo Token Plan API key (China region)
  XIAOMI_TOKEN_PLAN_AMS_API_KEY    - Xiaomi MiMo Token Plan API key (Amsterdam region)
  XIAOMI_TOKEN_PLAN_SGP_API_KEY    - Xiaomi MiMo Token Plan API key (Singapore region)
  AWS_PROFILE                      - AWS profile for Amazon Bedrock
  AWS_ACCESS_KEY_ID                - AWS access key for Amazon Bedrock
  AWS_SECRET_ACCESS_KEY            - AWS secret key for Amazon Bedrock
  AWS_BEARER_TOKEN_BEDROCK         - Bedrock API key (bearer token)
  AWS_REGION                       - AWS region for Amazon Bedrock (e.g., us-east-1)
  PI_AGENT_DIR                     - Config directory (default: ~/.pi/agent)
  PI_SESSION_DIR                   - Session storage directory (overridden by --session-dir)
  PI_PACKAGE_DIR                   - Override package directory (for Nix/Guix store paths)
  PI_OFFLINE                       - Disable startup network operations when set to 1/true/yes
  PI_TELEMETRY                     - Override install telemetry when set to 1/true/yes or 0/false/no
  PI_SHARE_VIEWER_URL              - Base URL for /share command (default: https://pi.dev/session/)

Built-in Tool Names:
  read   - Read file contents
  bash   - Execute bash commands
  edit   - Edit files with find/replace
  write  - Write files (creates/overwrites)
  grep   - Search file contents (read-only, off by default)
  find   - Find files by glob pattern (read-only, off by default)
  ls     - List directory contents (read-only, off by default)
`, app)
}
