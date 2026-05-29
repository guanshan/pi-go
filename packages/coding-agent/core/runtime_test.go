package core

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestCreateAgentSessionRuntimeAccessors(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	session := InMemorySession(cwd)
	runtime, err := CreateAgentSessionRuntime(context.Background(), testRuntimeFactory(t), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: session,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Session() == nil || runtime.Services() == nil {
		t.Fatalf("runtime=%#v", runtime)
	}
	if runtime.Cwd() != cwd {
		t.Fatalf("cwd=%q", runtime.Cwd())
	}
	if runtime.ModelFallbackMessage() != "" {
		t.Fatalf("fallback=%q", runtime.ModelFallbackMessage())
	}
	if len(runtime.Diagnostics()) != 0 {
		t.Fatalf("diagnostics=%#v", runtime.Diagnostics())
	}
}

func TestAgentSessionRuntimeNewSessionAndSwitchSession(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sessionDir := filepath.Join(agentDir, "sessions")
	initial, err := NewSessionManager(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(context.Background(), testRuntimeFactory(t), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: initial,
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidations := 0
	rebound := 0
	runtime.SetBeforeSessionInvalidate(func() { invalidations++ })
	runtime.SetRebindSession(func(*AgentSession) error {
		rebound++
		return nil
	})
	_, err = runtime.NewSession(context.Background(), NewSessionOptions{
		ParentSession: "parent.jsonl",
		Setup: func(ctx context.Context, sm *SessionManager) error {
			return sm.AppendSessionName("renamed")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if invalidations != 1 || rebound != 1 {
		t.Fatalf("invalidations=%d rebound=%d", invalidations, rebound)
	}
	if name := runtime.Session().Session.BuildContext().Name; name != "renamed" {
		t.Fatalf("name=%q", name)
	}
	switched, err := NewSessionManager(filepath.Join(cwd, "other"), sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := switched.AppendMessage(ai.NewUserMessage("hello", nil)); err != nil {
		t.Fatal(err)
	}
	_, err = runtime.SwitchSession(context.Background(), switched.File(), SwitchSessionOptions{CwdOverride: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if got := ai.MessageText(runtime.Session().Session.BuildContext().Messages[0]); got != "hello" {
		t.Fatalf("switched text=%q", got)
	}
	if invalidations != 2 || rebound != 2 {
		t.Fatalf("invalidations=%d rebound=%d", invalidations, rebound)
	}
	if runtime.Cwd() != cwd {
		t.Fatalf("cwd=%q", runtime.Cwd())
	}
}

func TestAgentSessionRuntimeForkAndImport(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sessionDir := filepath.Join(agentDir, "sessions")
	source, err := NewSessionManager(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.AppendMessage(ai.NewUserMessage("one", nil)); err != nil {
		t.Fatal(err)
	}
	if err := source.AppendMessage(ai.NewAssistantMessage("faux", "faux", "faux", ai.TextBlocks("reply"), ai.Usage{}, "stop")); err != nil {
		t.Fatal(err)
	}
	if err := source.AppendMessage(ai.NewUserMessage("two", nil)); err != nil {
		t.Fatal(err)
	}
	entryID := source.Entries[len(source.Entries)-1].ID
	runtime, err := CreateAgentSessionRuntime(context.Background(), testRuntimeFactory(t), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Fork(context.Background(), entryID, ForkOptions{Position: ForkPositionBefore})
	if err != nil {
		t.Fatal(err)
	}
	if result.SelectedText != "two" {
		t.Fatalf("selectedText=%q", result.SelectedText)
	}
	branch := runtime.Session().Session.BuildContext().Messages
	if len(branch) != 2 || ai.MessageText(branch[0]) != "one" || ai.MessageText(branch[1]) != "reply" {
		t.Fatalf("branch=%#v", branch)
	}

	importSource, err := NewSessionManager(filepath.Join(cwd, "import"), sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := importSource.AppendMessage(ai.NewUserMessage("imported", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportFromJsonl(context.Background(), importSource.File(), cwd); err != nil {
		t.Fatal(err)
	}
	if got := ai.MessageText(runtime.Session().Session.BuildContext().Messages[0]); got != "imported" {
		t.Fatalf("imported text=%q", got)
	}
	if runtime.Session().Session.File() == importSource.File() {
		t.Fatalf("expected imported session to be copied, got same file %q", runtime.Session().Session.File())
	}
}

func TestAgentSessionRuntimeImportMissingAndDispose(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	runtime, err := CreateAgentSessionRuntime(context.Background(), testRuntimeFactory(t), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: InMemorySession(cwd),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.ImportFromJsonl(context.Background(), "missing.jsonl", "")
	var notFound *SessionImportFileNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err=%v", err)
	}
	if err := runtime.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.Session() != nil || runtime.Services() != nil {
		t.Fatalf("runtime not disposed: %#v %#v", runtime.Session(), runtime.Services())
	}
	if _, err := runtime.NewSession(context.Background(), NewSessionOptions{}); err == nil {
		t.Fatal("expected disposed runtime to reject new session")
	}
}

func TestAgentSessionRuntimeReplacementCarriesLastActiveSelection(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	factory := func(ctx context.Context, options CreateAgentSessionRuntimeFactoryInput) (CreateAgentSessionRuntimeResult, error) {
		services, err := CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
			Cwd:      options.Cwd,
			AgentDir: options.AgentDir,
			ResourceLoaderOptions: DefaultResourceLoaderOptions{
				NoContextFiles:    true,
				NoExtensions:      true,
				NoSkills:          true,
				NoPromptTemplates: true,
				NoThemes:          true,
			},
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		model := options.LastActiveModel
		if model.Provider == "" {
			model = ai.Model{Provider: "faux", ID: "faux", API: "faux"}
		}
		thinking := options.LastActiveThinking
		if thinking == "" {
			thinking = ai.ThinkingOff
		}
		created, err := CreateAgentSessionFromServices(ctx, CreateAgentSessionFromServicesOptions{
			Services:       services,
			SessionManager: options.SessionManager,
			Model:          model,
			ThinkingLevel:  thinking,
			NoTools:        NoToolsAll,
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		return CreateAgentSessionRuntimeResult{CreateAgentSessionResult: created, Services: services}, nil
	}

	runtime, err := CreateAgentSessionRuntime(context.Background(), factory, CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: InMemorySession(cwd),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.Session().Model = ai.Model{Provider: "carried", ID: "model-x", API: "faux"}
	runtime.Session().ThinkingLevel = ai.ThinkingHigh

	if _, err := runtime.NewSession(context.Background(), NewSessionOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Session().Model; got.Provider != "carried" || got.ID != "model-x" {
		t.Fatalf("model=%#v", got)
	}
	if runtime.Session().ThinkingLevel != ai.ThinkingHigh {
		t.Fatalf("thinking=%s", runtime.Session().ThinkingLevel)
	}
}

func TestAgentSessionRuntimeEmitsExtensionLifecycleAroundReplacement(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	var events []string
	factory := func(ctx context.Context, options CreateAgentSessionRuntimeFactoryInput) (CreateAgentSessionRuntimeResult, error) {
		services, err := CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
			Cwd:      options.Cwd,
			AgentDir: options.AgentDir,
			ResourceLoaderOptions: DefaultResourceLoaderOptions{
				NoContextFiles:    true,
				NoExtensions:      true,
				NoSkills:          true,
				NoPromptTemplates: true,
				NoThemes:          true,
				ExtensionFactories: []coreext.Factory{
					func(api *coreext.API) error {
						api.On("session_start", func(payload any) {
							event := payload.(*coreext.SessionStartEvent)
							events = append(events, "start:"+string(event.Reason))
						})
						api.On("session_before_switch", func(payload any) {
							event := payload.(*coreext.SessionBeforeSwitchEvent)
							events = append(events, "before:"+string(event.Reason))
						})
						api.On("session_shutdown", func(payload any) {
							event := payload.(*coreext.SessionShutdownEvent)
							events = append(events, "shutdown:"+string(event.Reason))
						})
						api.OnShutdown(func(context.Context) error {
							events = append(events, "runtime_shutdown")
							return nil
						})
						return nil
					},
				},
			},
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		created, err := CreateAgentSessionFromServices(ctx, CreateAgentSessionFromServicesOptions{
			Services:       services,
			SessionManager: options.SessionManager,
			ScopedModels:   []ScopedModel{{Model: ai.Model{Provider: "faux", ID: "faux", API: "faux"}}},
			NoTools:        NoToolsAll,
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		return CreateAgentSessionRuntimeResult{CreateAgentSessionResult: created, Services: services}, nil
	}

	runtime, err := CreateAgentSessionRuntime(context.Background(), factory, CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: InMemorySession(cwd),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.SetBeforeSessionInvalidate(func() {
		events = append(events, "invalidate")
	})

	if !reflect.DeepEqual(events, []string{"start:startup"}) {
		t.Fatalf("startup events=%#v", events)
	}
	if _, err := runtime.NewSession(context.Background(), NewSessionOptions{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"start:startup", "before:new", "shutdown:new", "runtime_shutdown", "invalidate", "start:new"}) {
		t.Fatalf("replacement events=%#v", events)
	}
}

func TestAgentSessionRuntimeExtensionHooksCanCancelReplacement(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	factory := func(ctx context.Context, options CreateAgentSessionRuntimeFactoryInput) (CreateAgentSessionRuntimeResult, error) {
		services, err := CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
			Cwd:      options.Cwd,
			AgentDir: options.AgentDir,
			ResourceLoaderOptions: DefaultResourceLoaderOptions{
				NoContextFiles:    true,
				NoExtensions:      true,
				NoSkills:          true,
				NoPromptTemplates: true,
				NoThemes:          true,
				ExtensionFactories: []coreext.Factory{
					func(api *coreext.API) error {
						api.On("session_before_switch", func(payload any) {
							payload.(*coreext.SessionBeforeSwitchEvent).Cancel = true
						})
						api.On("session_before_fork", func(payload any) {
							payload.(*coreext.SessionBeforeForkEvent).Cancel = true
						})
						return nil
					},
				},
			},
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		created, err := CreateAgentSessionFromServices(ctx, CreateAgentSessionFromServicesOptions{
			Services:       services,
			SessionManager: options.SessionManager,
			ScopedModels:   []ScopedModel{{Model: ai.Model{Provider: "faux", ID: "faux", API: "faux"}}},
			NoTools:        NoToolsAll,
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		return CreateAgentSessionRuntimeResult{CreateAgentSessionResult: created, Services: services}, nil
	}

	source := InMemorySession(cwd)
	if err := source.AppendMessage(ai.NewUserMessage("one", nil)); err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(context.Background(), factory, CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	initialFile := runtime.Session().Session.File()
	result, err := runtime.NewSession(context.Background(), NewSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cancelled {
		t.Fatal("expected extension to cancel new session")
	}
	if runtime.Session().Session.File() != initialFile {
		t.Fatal("session changed despite cancelled replacement")
	}
	entryID := source.Entries[len(source.Entries)-1].ID
	forked, err := runtime.Fork(context.Background(), entryID, ForkOptions{Position: ForkPositionAt})
	if err != nil {
		t.Fatal(err)
	}
	if !forked.Cancelled {
		t.Fatal("expected extension to cancel fork")
	}
}

func testRuntimeFactory(t *testing.T) CreateAgentSessionRuntimeFactory {
	t.Helper()
	return func(ctx context.Context, options CreateAgentSessionRuntimeFactoryInput) (CreateAgentSessionRuntimeResult, error) {
		services, err := CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
			Cwd:      options.Cwd,
			AgentDir: options.AgentDir,
			ResourceLoaderOptions: DefaultResourceLoaderOptions{
				NoContextFiles:    true,
				NoExtensions:      true,
				NoSkills:          true,
				NoPromptTemplates: true,
				NoThemes:          true,
			},
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		created, err := CreateAgentSessionFromServices(ctx, CreateAgentSessionFromServicesOptions{
			Services:       services,
			SessionManager: options.SessionManager,
			ScopedModels:   []ScopedModel{{Model: ai.Model{Provider: "faux", ID: "faux", API: "faux"}}},
			NoTools:        NoToolsAll,
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		return CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: created,
			Services:                 services,
			Diagnostics:              services.Diagnostics,
		}, nil
	}
}
