package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func Main(ctx context.Context, argv []string) error {
	return MainWithOptions(ctx, argv, MainOptions{})
}

type MainOptions struct {
	ExtensionFactories []coreext.Factory
}

func MainWithOptions(ctx context.Context, argv []string, options MainOptions) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cwd, _ = AbsPath(cwd)
	agentDir := AgentDir()
	settings := NewSettingsManager(cwd, agentDir)

	if handled, err := HandlePackageCommand(argv, cwd, agentDir, settings); handled || err != nil {
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

	if args.APIKey != "" {
		provider := args.Provider
		if provider == "" && strings.Contains(args.Model, "/") {
			provider = strings.SplitN(args.Model, "/", 2)[0]
		}
		if provider == "" {
			provider = settings.DefaultProvider()
		}
		if provider != "" {
			auth.SetRuntime(provider, args.APIKey)
		}
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
	if err := validateSessionCWD(session, args.NoSession); err != nil {
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
	if !ok {
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
	scopedModels := resolveScopedModels(registry, args.Models)
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
			os.Exit(exit)
		}
		return err
	}
	return RunInteractiveMode(ctx, runtime, initial.Message, initial.Images, os.Stdin, os.Stdout, os.Stderr, initial.Remaining)
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
		path, err := ResolveSessionPath(args.Session, cwd, sessionDir)
		if err != nil {
			return nil, err
		}
		return OpenSession(path, cwd)
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

func validateSessionCWD(session *SessionManager, allowMissing bool) error {
	if session == nil || allowMissing {
		return nil
	}
	cwd := strings.TrimSpace(session.CWD())
	if cwd == "" {
		return nil
	}
	info, err := os.Stat(cwd)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session cwd no longer exists: %s", cwd)
		}
		return fmt.Errorf("cannot access session cwd %s: %w", cwd, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("session cwd is not a directory: %s", cwd)
	}
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

func resolveScopedModels(registry *ai.ModelRegistry, values []string) []ScopedModel {
	if registry == nil || len(values) == 0 {
		return nil
	}
	resolved := make([]ScopedModel, 0, len(values))
	seen := map[string]struct{}{}
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
		provider := ""
		modelID := candidate
		if strings.Contains(candidate, "/") {
			parts := strings.SplitN(candidate, "/", 2)
			provider = parts[0]
			modelID = parts[1]
		}
		model, ok, _ := registry.Match(provider, modelID)
		if !ok {
			continue
		}
		key := model.Provider + "/" + model.ID + ":" + string(thinking)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		resolved = append(resolved, ScopedModel{Model: model, ThinkingLevel: thinking})
	}
	return resolved
}
