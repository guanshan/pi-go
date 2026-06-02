package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/guanshan/pi-go/packages/ai"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func Main(ctx context.Context, argv []string) error {
	return MainWithOptions(ctx, argv, MainOptions{})
}

type MainOptions struct {
	ExtensionFactories []coreext.Factory
	// PackageManagerFactory supplies the full package manager for CLI
	// install/remove/update. When nil, the legacy in-package installer is used.
	PackageManagerFactory PackageManagerFactory
	// Shutdown installs OS-signal-driven shutdown once the agent runtime exists.
	// It receives a dispose callback to run before the process exits and returns
	// a stop func that uninstalls the handler. Supplied by the binary layer so
	// core stays free of platform-specific signal code. When nil, the default
	// OS signal behavior applies.
	Shutdown ShutdownInstaller
}

// ShutdownInstaller wires a runtime-dispose callback into signal handling. See
// MainOptions.Shutdown.
type ShutdownInstaller func(dispose func()) (stop func())

func MainWithOptions(ctx context.Context, argv []string, options MainOptions) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cwd, _ = AbsPath(cwd)
	agentDir := AgentDir()
	settings := NewSettingsManager(cwd, agentDir)

	if handled, err := HandlePackageCommand(argv, cwd, agentDir, settings, options.PackageManagerFactory); handled || err != nil {
		return err
	}

	args := cli.ParseArgs(argv)
	for _, d := range args.Diagnostics {
		if d.Type == "error" {
			fmt.Fprintln(os.Stderr, "Error:", d.Message)
		} else {
			fmt.Fprintln(os.Stderr, "Warning:", d.Message)
		}
	}
	for _, d := range args.Diagnostics {
		if d.Type == "error" {
			return fmt.Errorf("invalid arguments")
		}
	}
	if args.Offline {
		_ = os.Setenv("PI_OFFLINE", "1")
		_ = os.Setenv("PI_SKIP_VERSION_CHECK", "1")
	}
	if args.Version {
		fmt.Println(Version)
		return nil
	}
	if args.Export != "" {
		output := ""
		if len(args.Messages) > 0 {
			output = args.Messages[0]
		}
		result, err := ExportSessionToHTML(args.Export, output)
		if err != nil {
			return err
		}
		fmt.Println("Exported to:", result)
		return nil
	}
	if err := validateRuntimeArgs(args); err != nil {
		return err
	}
	resourceOptions := DefaultResourceLoaderOptions{
		AdditionalExtensionPaths:      append([]string(nil), args.Extensions...),
		AdditionalSkillPaths:          append([]string(nil), args.Skills...),
		AdditionalPromptTemplatePaths: append([]string(nil), args.PromptTemplates...),
		AdditionalThemePaths:          append([]string(nil), args.Themes...),
		ExtensionFactories:            append([]coreext.Factory(nil), options.ExtensionFactories...),
		NoExtensions:                  args.NoExtensions,
		NoSkills:                      args.NoSkills,
		NoPromptTemplates:             args.NoPromptTemplates,
		NoThemes:                      args.NoThemes,
		NoContextFiles:                args.NoContextFiles,
		SystemPrompt:                  args.SystemPrompt,
		AppendSystemPrompt:            append([]string(nil), args.AppendSystemPrompt...),
	}
	services, err := CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
		Cwd:                   cwd,
		AgentDir:              agentDir,
		SettingsManager:       settings,
		ResourceLoaderOptions: resourceOptions,
		ExtensionFlagValues:   args.UnknownFlags,
	})
	if err != nil {
		return err
	}
	settings = services.SettingsManager
	auth := services.AuthStorage
	registry := services.ModelRegistry
	for _, diagnostic := range services.Diagnostics {
		fmt.Fprintln(os.Stderr, capitalizeASCII(string(diagnostic.Type))+":", diagnostic.Message)
	}

	// Help is printed after extensions load so extension-declared CLI flags are
	// included, matching the TypeScript ordering.
	if args.Help {
		cli.PrintHelp(os.Stdout, extensionFlagHelp(services.ExtensionRuntime))
		return nil
	}

	if args.ListModels != nil {
		cli.PrintModels(os.Stdout, registry, *args.ListModels)
		return nil
	}

	sessionDir := settings.SessionDir()
	if args.SessionDir != "" {
		sessionDir = ExpandTilde(args.SessionDir)
	}
	session, err := createSession(args, cwd, sessionDir, settings, os.Stdin, os.Stderr)
	if err != nil {
		if errors.Is(err, cli.ErrSessionSelectionCancelled) {
			return nil
		}
		return err
	}
	if err := validateSessionCWD(session, cwd, isNonInteractiveMode(args), os.Stdin, os.Stderr); err != nil {
		if errors.Is(err, cli.ErrSessionSelectionCancelled) {
			return nil
		}
		return err
	}
	if session.CWD() != "" && session.CWD() != services.Cwd {
		services, err = CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
			Cwd:                   session.CWD(),
			AgentDir:              agentDir,
			AuthStorage:           auth,
			ResourceLoaderOptions: resourceOptions,
			ExtensionFlagValues:   args.UnknownFlags,
		})
		if err != nil {
			return err
		}
		settings = services.SettingsManager
		auth = services.AuthStorage
		registry = services.ModelRegistry
		for _, diagnostic := range services.Diagnostics {
			fmt.Fprintln(os.Stderr, capitalizeASCII(string(diagnostic.Type))+":", diagnostic.Message)
		}
	}
	if name := strings.TrimSpace(args.Name); name != "" {
		if err := session.AppendSessionName(name); err != nil {
			return err
		}
	} else if args.Name != "" {
		return fmt.Errorf("session name cannot be empty")
	}
	sessionCtx := session.BuildContext()

	model, ok, warning := InitialModel(registry, args, settings)
	if sessionCtx.ModelProvider != "" && sessionCtx.ModelID != "" && args.Model == "" {
		if restored, found := registry.Find(sessionCtx.ModelProvider, sessionCtx.ModelID); found && registry.HasAuth(restored) {
			model = restored
			ok = true
		}
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, "Warning:", warning)
	}
	// --api-key binds the supplied key to the RESOLVED session model's provider,
	// so it must run after model resolution. Without a resolved model there is no
	// provider to bind to, which is a fatal error. Mirrors TS main.ts:641-651.
	if args.APIKey != "" {
		if !ok {
			return fmt.Errorf("--api-key requires a model to be specified via --model, --provider/--model, or --models")
		}
		auth.SetRuntime(model.Provider, args.APIKey)
	}
	// When no real model resolved, non-interactive modes (print/json/rpc) must not
	// silently run against a faux model: error with the TS no-model guidance and
	// exit non-zero. Interactive mode keeps the faux fallback so the user can pick
	// a model via /model. Explicit "--model faux/faux" sets ok=true above, so it is
	// unaffected. Mirrors TS main.ts:731-734.
	if !ok {
		if isNonInteractiveMode(args) {
			return fmt.Errorf("%s", formatNoModelsAvailableMessage())
		}
		model = ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	}
	thinking := settings.DefaultThinkingLevel()
	if len(sessionCtx.Messages) > 0 && sessionBranchHasThinkingChange(session) && sessionCtx.ThinkingLevel != "" {
		thinking = sessionCtx.ThinkingLevel
	}
	if args.HasThinking {
		thinking = args.Thinking
	} else if _, level, has := splitModelThinking(args.Model); has {
		thinking = level
	}
	thinking = ClampThinking(model, thinking)
	noToolsMode := NoToolsNone
	if args.NoTools {
		noToolsMode = NoToolsAll
	} else if args.NoBuiltinTools {
		noToolsMode = NoToolsBuiltin
	}
	toolNames := append([]string(nil), args.Tools...)
	excludeToolNames := append([]string(nil), args.ExcludeTools...)
	scopedModels, scopedWarnings := resolveScopedModels(registry, args.Models)
	for _, scopedWarning := range scopedWarnings {
		fmt.Fprintln(os.Stderr, "Warning:", scopedWarning)
	}
	var runtime *AgentSessionRuntime
	runtime, err = CreateAgentSessionRuntime(ctx, func(ctx context.Context, options CreateAgentSessionRuntimeFactoryInput) (CreateAgentSessionRuntimeResult, error) {
		activeModel := model
		activeThinking := thinking
		if options.LastActiveModel.Provider != "" {
			activeModel = options.LastActiveModel
		}
		if options.LastActiveThinking != "" {
			activeThinking = options.LastActiveThinking
		}

		nextServices := services
		if nextServices == nil || nextServices.Cwd != options.Cwd || nextServices.AgentDir != options.AgentDir || options.SessionManager != session {
			var err error
			nextServices, err = CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
				Cwd:                   options.Cwd,
				AgentDir:              options.AgentDir,
				AuthStorage:           auth,
				ResourceLoaderOptions: resourceOptions,
			})
			if err != nil {
				return CreateAgentSessionRuntimeResult{}, err
			}
		}

		created, err := CreateAgentSessionFromServices(ctx, CreateAgentSessionFromServicesOptions{
			Services:       nextServices,
			SessionManager: options.SessionManager,
			Model:          activeModel,
			ThinkingLevel:  activeThinking,
			ScopedModels:   append([]ScopedModel(nil), scopedModels...),
			Tools:          append([]string(nil), toolNames...),
			ExcludeTools:   append([]string(nil), excludeToolNames...),
			NoTools:        noToolsMode,
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		return CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: created,
			Services:                 nextServices,
			Diagnostics:              nextServices.Diagnostics,
		}, nil
	}, CreateAgentSessionRuntimeOptions{
		Cwd:            firstNonEmpty(session.CWD(), cwd),
		AgentDir:       agentDir,
		SessionManager: session,
	})
	if err != nil {
		return err
	}

	// An error-level diagnostic (e.g. an explicitly requested extension that
	// failed to load) is fatal: abort before creating a session turn or calling
	// the provider, mirroring TS reportDiagnostics + process.exit(1) (main.ts:
	// 725-727). Diagnostics were already printed during service creation above;
	// here we only enforce the non-zero exit. --help and --list-models return
	// earlier, so they stay lenient as in TS.
	if err := fatalDiagnostic(runtime.Diagnostics()); err != nil {
		// Dispose synchronously here since we return before installing the defer
		// below would matter; the deferred guard is harmless if unset.
		_ = runtime.Dispose(ctx)
		return err
	}

	// Dispose the runtime exactly once on any exit path so extension/session
	// shutdown hooks (session_shutdown) always fire — on normal return and on
	// the signal path below. Mirrors the dispose/shutdown finally blocks in the
	// TS print/rpc/interactive modes.
	var disposeOnce sync.Once
	disposeRuntime := func() {
		disposeOnce.Do(func() { _ = runtime.Dispose(ctx) })
	}
	defer disposeRuntime()
	if options.Shutdown != nil {
		stop := options.Shutdown(disposeRuntime)
		defer stop()
	}

	if args.Mode == cli.ModeRPC {
		return RunRPC(ctx, runtime, os.Stdin, os.Stdout)
	}

	stdinContent := ""
	if !isInputTTY() {
		data, _ := io.ReadAll(os.Stdin)
		stdinContent = strings.TrimSpace(string(data))
		if stdinContent != "" && !args.Print && args.Mode != cli.ModeJSON {
			args.Print = true
		}
	}
	initial, err := PrepareInitialPrompt(session.CWD(), args, stdinContent, settings.ImageAutoResize())
	if err != nil {
		return err
	}
	if args.Print || args.Mode == cli.ModeJSON {
		exit, err := RunPrintMode(ctx, runtime, args.Mode, initial.Message, initial.Images, os.Stdout, os.Stderr, initial.Remaining)
		if exit != 0 {
			// os.Exit skips deferred disposal, so dispose explicitly first.
			disposeRuntime()
			os.Exit(exit)
		}
		return err
	}
	return RunInteractiveMode(ctx, runtime, initial.Message, initial.Images, os.Stdin, os.Stdout, os.Stderr, initial.Remaining)
}

// fatalDiagnostic returns a non-nil error if any diagnostic is error-level, so
// the caller can abort with a non-zero exit. The individual diagnostic messages
// are already printed at service-creation time, so the returned error is a short
// summary that avoids re-printing the full message.
func fatalDiagnostic(diagnostics []Diagnostic) error {
	for _, diagnostic := range diagnostics {
		if diagnostic.Type == DiagError {
			return fmt.Errorf("failed to load required resources")
		}
	}
	return nil
}

// isNonInteractiveMode reports whether the CLI runs in a mode that never prompts
// the user (print/json/rpc), matching the TS `appMode !== "interactive"` checks.
func isNonInteractiveMode(args cli.Args) bool {
	return args.Print || args.Mode == cli.ModeJSON || args.Mode == cli.ModeRPC
}

// capitalizeASCII upper-cases the first byte of s. It is used for short,
// ASCII diagnostic labels ("error", "warning") where strings.Title would be
// overkill (and is deprecated).
func capitalizeASCII(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// extensionFlagHelp formats the CLI flags declared by loaded extensions for the
// help output. cli.PrintHelp prepends "--" to each entry, so only the flag name
// (with its description) is returned here.
func extensionFlagHelp(runtime *coreext.Runner) []string {
	flags := runtime.RegisteredFlags()
	if len(flags) == 0 {
		return nil
	}
	out := make([]string, 0, len(flags))
	for _, flag := range flags {
		entry := flag.Name
		if flag.Description != "" {
			entry = fmt.Sprintf("%-28s %s", flag.Name, flag.Description)
		}
		out = append(out, entry)
	}
	return out
}

func validateRuntimeArgs(args cli.Args) error {
	if args.Mode == cli.ModeRPC && len(args.FileArgs) > 0 {
		return fmt.Errorf("@file arguments are not supported in RPC mode")
	}
	if args.SessionID != "" {
		if !ValidSessionID(args.SessionID) {
			return fmt.Errorf("session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character")
		}
		// --session-id intentionally does NOT conflict with --fork: TS treats
		// --fork --session-id as "fork into a new session with this explicit
		// id", so the fork branch in createSession handles that combination.
		var conflicts []string
		for _, c := range []struct {
			name string
			set  bool
		}{
			{"--session", args.Session != ""},
			{"--continue", args.Continue},
			{"--resume", args.Resume},
			{"--no-session", args.NoSession},
		} {
			if c.set {
				conflicts = append(conflicts, c.name)
			}
		}
		if len(conflicts) > 0 {
			return fmt.Errorf("--session-id cannot be combined with %s", strings.Join(conflicts, ", "))
		}
	}
	if args.Fork == "" {
		return nil
	}
	var conflicts []string
	if args.Session != "" {
		conflicts = append(conflicts, "--session")
	}
	if args.Continue {
		conflicts = append(conflicts, "--continue")
	}
	if args.Resume {
		conflicts = append(conflicts, "--resume")
	}
	if args.NoSession {
		conflicts = append(conflicts, "--no-session")
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("--fork cannot be combined with %s", strings.Join(conflicts, ", "))
	}
	return nil
}

func createSession(args cli.Args, cwd, sessionDir string, settings *SettingsManager, stdin io.Reader, stderr io.Writer) (*SessionManager, error) {
	if args.NoSession {
		return InMemorySession(cwd), nil
	}
	if args.Fork != "" {
		// --fork --session-id: the explicit id becomes the forked session's id,
		// but only if no local session already claims it (mirrors TS).
		if args.SessionID != "" {
			if existing, err := findLocalSessionByExactID(cwd, sessionDir, args.SessionID); err == nil && existing != "" {
				return nil, fmt.Errorf("session already exists with id %q", args.SessionID)
			}
		}
		path, err := ResolveSessionPath(args.Fork, cwd, sessionDir)
		if err != nil {
			return nil, err
		}
		return ForkSessionWithID(path, cwd, sessionDir, args.SessionID)
	}
	if args.Session != "" {
		resolved := ResolveSession(args.Session, cwd, sessionDir)
		switch resolved.Type {
		case ResolvedSessionPathType, ResolvedSessionLocal:
			return OpenSession(resolved.Path, cwd)
		case ResolvedSessionGlobal:
			// A cross-project hit must NOT be silently opened. Non-interactive modes
			// get a clear error; interactive callers are prompted to fork the session
			// into the current project, aborting cleanly if declined (mirrors TS
			// main.ts:288-300).
			if isNonInteractiveMode(args) {
				return nil, fmt.Errorf("session found in different project: %s\nRe-run from that directory, or use --fork to copy it into the current project", resolved.CWD)
			}
			fmt.Fprintf(stderr, "Session found in different project: %s\n", resolved.CWD)
			confirmed, err := cli.Confirm(stdin, stderr, "Fork this session into current directory?")
			if err != nil {
				return nil, err
			}
			if !confirmed {
				fmt.Fprintln(stderr, "Aborted.")
				return nil, cli.ErrSessionSelectionCancelled
			}
			return ForkSessionWithID(resolved.Path, cwd, sessionDir, "")
		default:
			return nil, fmt.Errorf("no session found matching %q", resolved.Arg)
		}
	}
	if args.Continue {
		return ContinueRecent(cwd, sessionDir)
	}
	if args.Resume {
		sessions, err := resumeSessions(cwd, sessionDir)
		if err != nil {
			return nil, err
		}
		if len(sessions) == 0 {
			return nil, fmt.Errorf("no sessions found")
		}
		if isInputTTY() {
			path, err := cli.SelectSession(stdin, stderr, sessionChoices(sessions))
			if err != nil {
				return nil, err
			}
			return OpenSession(path, cwd)
		}
		return OpenSession(sessions[0].Path, cwd)
	}
	if args.SessionID != "" {
		// A plain --session-id that matches an existing local session opens it
		// (resume by id); otherwise a fresh session is created with that id.
		if existing, err := findLocalSessionByExactID(cwd, sessionDir, args.SessionID); err == nil && existing != "" {
			return OpenSession(existing, cwd)
		}
		return NewSessionManagerWithID(cwd, sessionDir, args.SessionID)
	}
	return NewSessionManager(cwd, sessionDir)
}

// validateSessionCWD enforces that a stored session's working directory still
// exists, mirroring TS getMissingSessionCwdIssue + MissingSessionCwdError
// (session-cwd.ts). In-memory sessions (no session file) are skipped, matching
// the TS `!sessionFile` early return. When the stored cwd is missing,
// non-interactive callers get the TS-worded error; interactive callers are
// prompted to continue in the current cwd (mirroring TS
// formatMissingSessionCwdPrompt + the Continue/Cancel selector), and on confirm
// the session's runtime cwd is overridden to fallbackCwd without rewriting the
// session file. Declining aborts cleanly via cli.ErrSessionSelectionCancelled.
func validateSessionCWD(session *SessionManager, fallbackCwd string, nonInteractive bool, stdin io.Reader, stderr io.Writer) error {
	if session == nil {
		return nil
	}
	sessionFile := session.File()
	if sessionFile == "" {
		return nil
	}
	sessionCwd := strings.TrimSpace(session.CWD())
	if sessionCwd == "" {
		return nil
	}
	if _, err := os.Stat(sessionCwd); err == nil {
		return nil
	}
	if nonInteractive {
		return fmt.Errorf("Stored session working directory does not exist: %s\nSession file: %s\nCurrent working directory: %s", sessionCwd, sessionFile, fallbackCwd)
	}
	fmt.Fprintf(stderr, "cwd from session file does not exist\n%s\n\ncontinue in current cwd\n%s\n", sessionCwd, fallbackCwd)
	confirmed, err := cli.Confirm(stdin, stderr, "Continue in current cwd?")
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintln(stderr, "Aborted.")
		return cli.ErrSessionSelectionCancelled
	}
	session.OverrideCWD(fallbackCwd)
	return nil
}

func resumeSessions(cwd, sessionDir string) ([]SessionInfo, error) {
	local, err := ListSessions(cwd, sessionDir)
	if err != nil {
		return nil, err
	}
	all, err := ListAllSessions(sessionDir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]SessionInfo, 0, len(local)+len(all))
	for _, session := range append(local, all...) {
		key := session.Path
		if key == "" {
			key = session.ID
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, session)
	}
	sortSessions(out)
	return out, nil
}

func sessionChoices(sessions []SessionInfo) []cli.SessionChoice {
	choices := make([]cli.SessionChoice, 0, len(sessions))
	for _, session := range sessions {
		choices = append(choices, cli.SessionChoice{
			ID:        session.ID,
			Path:      session.Path,
			Name:      session.Name,
			Preview:   session.Preview,
			UpdatedAt: session.UpdatedAt,
		})
	}
	return choices
}

func splitModelThinking(model string) (string, ai.ThinkingLevel, bool) {
	idx := strings.LastIndex(model, ":")
	if idx < 0 {
		return model, "", false
	}
	level := model[idx+1:]
	if !ai.IsValidThinkingLevel(level) {
		return model, "", false
	}
	return model[:idx], ai.ThinkingLevel(level), true
}

// resolveScopedModels resolves --models patterns to scoped models, returning any
// per-pattern warnings (e.g. no-match). Glob patterns (containing *, ?, or [)
// expand to ALL matching available models — matched against both "provider/id"
// and the bare "id" — with the optional :thinking suffix applied to every
// expanded model. Non-glob patterns resolve to a single best match. Mirrors TS
// resolveModelScope (model-resolver.ts:255).
func resolveScopedModels(registry *ai.ModelRegistry, values []string) ([]ScopedModel, []string) {
	if registry == nil || len(values) == 0 {
		return nil, nil
	}
	var available []ai.Model
	resolved := make([]ScopedModel, 0, len(values))
	var warnings []string
	seen := map[string]struct{}{}
	add := func(model ai.Model, thinking ai.ThinkingLevel) {
		key := model.Provider + "/" + model.ID + ":" + string(thinking)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		resolved = append(resolved, ScopedModel{Model: model, ThinkingLevel: thinking})
	}
	for _, value := range values {
		candidate := strings.TrimSpace(value)
		if candidate == "" {
			continue
		}
		thinking := ai.ThinkingLevel("")
		if modelName, level, ok := splitModelThinking(candidate); ok {
			candidate = modelName
			thinking = level
		}
		if isGlobPattern(candidate) {
			if available == nil {
				available = registry.AvailableConfigured()
			}
			matches := globMatchModels(available, candidate)
			if len(matches) == 0 {
				warnings = append(warnings, fmt.Sprintf("No models match pattern %q", value))
				continue
			}
			for _, model := range matches {
				add(model, thinking)
			}
			continue
		}
		provider := ""
		modelID := candidate
		if strings.Contains(candidate, "/") {
			parts := strings.SplitN(candidate, "/", 2)
			provider = parts[0]
			modelID = parts[1]
		}
		model, ok, _ := registry.Match(provider, modelID)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("No models match pattern %q", value))
			continue
		}
		add(model, thinking)
	}
	return resolved, warnings
}

// isGlobPattern reports whether a model pattern uses glob syntax, matching the
// TS check (model-resolver.ts) for `*`, `?`, or `[`.
func isGlobPattern(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// globMatchModels returns every model whose "provider/id" or bare "id" matches
// the glob pattern case-insensitively, preserving registry order. Bracket
// classes are not fully supported (GlobToRegexp only expands * and ?).
func globMatchModels(models []ai.Model, pattern string) []ai.Model {
	re := aiutils.GlobToRegexp(strings.ToLower(pattern))
	var out []ai.Model
	for _, model := range models {
		fullID := strings.ToLower(model.Provider + "/" + model.ID)
		id := strings.ToLower(model.ID)
		if re.MatchString(fullID) || re.MatchString(id) {
			out = append(out, model)
		}
	}
	return out
}
