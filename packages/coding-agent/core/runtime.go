package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

type CreateAgentSessionRuntimeFactory func(ctx context.Context, options CreateAgentSessionRuntimeFactoryInput) (CreateAgentSessionRuntimeResult, error)

type CreateAgentSessionRuntimeFactoryInput struct {
	Cwd                string
	AgentDir           string
	SessionManager     *SessionManager
	LastActiveModel    ai.Model
	LastActiveThinking ai.ThinkingLevel
}

type CreateAgentSessionRuntimeResult struct {
	CreateAgentSessionResult
	Services    *AgentSessionServices
	Diagnostics []Diagnostic
}

type CreateAgentSessionRuntimeOptions struct {
	Cwd            string
	AgentDir       string
	SessionManager *SessionManager
}

type SessionImportFileNotFoundError struct{ Path string }

func (e *SessionImportFileNotFoundError) Error() string {
	return fmt.Sprintf("session import file not found: %s", e.Path)
}

type AgentSessionRuntime struct {
	mu sync.Mutex

	session                 *AgentSession
	services                *AgentSessionServices
	diagnostics             []Diagnostic
	modelFallbackMessage    string
	lastActiveModel         ai.Model
	lastActiveThinking      ai.ThinkingLevel
	createRuntime           CreateAgentSessionRuntimeFactory
	rebindSession           func(*AgentSession) error
	beforeSessionInvalidate func()
}

type ReplacedSessionContext struct {
	Session  *AgentSession
	Services *AgentSessionServices
}

type SessionReplacementResult struct {
	Cancelled    bool
	SelectedText string
}

type SwitchSessionOptions struct {
	CwdOverride string
	WithSession func(ctx context.Context, c ReplacedSessionContext) error
}

type NewSessionOptions struct {
	ParentSession string
	Setup         func(ctx context.Context, sm *SessionManager) error
	WithSession   func(ctx context.Context, c ReplacedSessionContext) error
}

type ForkPosition string

const (
	ForkPositionBefore ForkPosition = "before"
	ForkPositionAt     ForkPosition = "at"
)

type ForkOptions struct {
	Position    ForkPosition
	WithSession func(ctx context.Context, c ReplacedSessionContext) error
}

func CreateAgentSessionRuntime(ctx context.Context, createRuntime CreateAgentSessionRuntimeFactory, options CreateAgentSessionRuntimeOptions) (*AgentSessionRuntime, error) {
	if createRuntime == nil {
		return nil, fmt.Errorf("create runtime factory is required")
	}
	result, err := createRuntime(ctx, CreateAgentSessionRuntimeFactoryInput{
		Cwd:            options.Cwd,
		AgentDir:       options.AgentDir,
		SessionManager: options.SessionManager,
	})
	if err != nil {
		return nil, err
	}
	if result.Session == nil {
		return nil, fmt.Errorf("create runtime factory returned nil session")
	}
	if result.Services == nil {
		return nil, fmt.Errorf("create runtime factory returned nil services")
	}
	runtime := &AgentSessionRuntime{
		session:              result.Session,
		services:             result.Services,
		diagnostics:          append([]Diagnostic(nil), result.Diagnostics...),
		modelFallbackMessage: result.ModelFallbackMessage,
		lastActiveModel:      result.Session.Model,
		lastActiveThinking:   result.Session.ThinkingLevel,
		createRuntime:        createRuntime,
	}
	if runtime.session != nil {
		runtime.session.emitExtensionSessionStart(coreext.SessionStartStartup, "")
	}
	return runtime, nil
}

func (r *AgentSessionRuntime) Session() *AgentSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

func (r *AgentSessionRuntime) Services() *AgentSessionServices {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.services
}

func (r *AgentSessionRuntime) Cwd() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.services != nil && r.services.Cwd != "" {
		return r.services.Cwd
	}
	if r.session != nil && r.session.Session != nil {
		return r.session.Session.CWD()
	}
	return ""
}

func (r *AgentSessionRuntime) Diagnostics() []Diagnostic {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Diagnostic(nil), r.diagnostics...)
}

func (r *AgentSessionRuntime) ModelFallbackMessage() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.modelFallbackMessage
}

func (r *AgentSessionRuntime) SetRebindSession(fn func(*AgentSession) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rebindSession = fn
}

func (r *AgentSessionRuntime) SetBeforeSessionInvalidate(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beforeSessionInvalidate = fn
}

func (r *AgentSessionRuntime) SwitchSession(ctx context.Context, sessionPath string, opts SwitchSessionOptions) (SessionReplacementResult, error) {
	factory, currentSession, services, before, rebind, lastModel, lastThinking, err := r.snapshot()
	if err != nil {
		return SessionReplacementResult{}, err
	}
	resolved, err := ResolveSessionPath(sessionPath, services.Cwd, services.SettingsManager.SessionDir())
	if err != nil {
		return SessionReplacementResult{}, err
	}
	if currentSession != nil && currentSession.shouldCancelSessionSwitch(coreext.SessionStartResume, resolved) {
		return SessionReplacementResult{Cancelled: true}, nil
	}
	fallbackCWD := firstNonEmpty(opts.CwdOverride, services.Cwd)
	opened, err := OpenSession(resolved, fallbackCWD)
	if err != nil {
		return SessionReplacementResult{}, err
	}
	if opts.CwdOverride != "" {
		opened.Header.CWD = opts.CwdOverride
	}
	created, err := factory(ctx, CreateAgentSessionRuntimeFactoryInput{
		Cwd:                opened.CWD(),
		AgentDir:           services.AgentDir,
		SessionManager:     opened,
		LastActiveModel:    lastModel,
		LastActiveThinking: lastThinking,
	})
	if err != nil {
		return SessionReplacementResult{}, err
	}
	return r.applyReplacement(ctx, currentSession, created, coreext.SessionShutdownResume, coreext.SessionStartResume, before, rebind, opts.WithSession, SessionReplacementResult{})
}

func (r *AgentSessionRuntime) NewSession(ctx context.Context, opts NewSessionOptions) (SessionReplacementResult, error) {
	factory, currentSession, services, before, rebind, lastModel, lastThinking, err := r.snapshot()
	if err != nil {
		return SessionReplacementResult{}, err
	}
	createdSession, err := NewSessionManager(services.Cwd, services.SettingsManager.SessionDir())
	if err != nil {
		return SessionReplacementResult{}, err
	}
	if opts.ParentSession != "" {
		createdSession.Header.ParentSession = opts.ParentSession
	}
	if opts.Setup != nil {
		if err := opts.Setup(ctx, createdSession); err != nil {
			return SessionReplacementResult{}, err
		}
	}
	if _, err := createdSession.rewrite(); err != nil {
		return SessionReplacementResult{}, err
	}
	if currentSession != nil && currentSession.shouldCancelSessionSwitch(coreext.SessionStartNew, createdSession.File()) {
		return SessionReplacementResult{Cancelled: true}, nil
	}
	created, err := factory(ctx, CreateAgentSessionRuntimeFactoryInput{
		Cwd:                createdSession.CWD(),
		AgentDir:           services.AgentDir,
		SessionManager:     createdSession,
		LastActiveModel:    lastModel,
		LastActiveThinking: lastThinking,
	})
	if err != nil {
		return SessionReplacementResult{}, err
	}
	return r.applyReplacement(ctx, currentSession, created, coreext.SessionShutdownNew, coreext.SessionStartNew, before, rebind, opts.WithSession, SessionReplacementResult{})
}

func (r *AgentSessionRuntime) Fork(ctx context.Context, entryID string, opts ForkOptions) (SessionReplacementResult, error) {
	factory, currentSession, services, before, rebind, lastModel, lastThinking, err := r.snapshot()
	if err != nil {
		return SessionReplacementResult{}, err
	}
	if r.Session() == nil || r.Session().Session == nil {
		return SessionReplacementResult{}, fmt.Errorf("runtime has no active session")
	}
	if currentSession != nil && currentSession.shouldCancelSessionFork(entryID, opts.Position) {
		return SessionReplacementResult{Cancelled: true}, nil
	}
	source := r.Session().Session
	forked, selectedText, err := forkSessionAtPosition(source, entryID, opts.Position, services.SettingsManager.SessionDir())
	if err != nil {
		return SessionReplacementResult{}, err
	}
	created, err := factory(ctx, CreateAgentSessionRuntimeFactoryInput{
		Cwd:                forked.CWD(),
		AgentDir:           services.AgentDir,
		SessionManager:     forked,
		LastActiveModel:    lastModel,
		LastActiveThinking: lastThinking,
	})
	if err != nil {
		return SessionReplacementResult{}, err
	}
	return r.applyReplacement(ctx, currentSession, created, coreext.SessionShutdownFork, coreext.SessionStartFork, before, rebind, opts.WithSession, SessionReplacementResult{SelectedText: selectedText})
}

func (r *AgentSessionRuntime) ImportFromJsonl(ctx context.Context, inputPath string, cwdOverride string) (SessionReplacementResult, error) {
	factory, currentSession, services, before, rebind, lastModel, lastThinking, err := r.snapshot()
	if err != nil {
		return SessionReplacementResult{}, err
	}
	resolved := inputPath
	if !filepath.IsAbs(ExpandTilde(resolved)) {
		resolved = ResolveInCWD(services.Cwd, resolved)
	} else {
		resolved = ExpandTilde(resolved)
	}
	if _, err := os.Stat(resolved); err != nil {
		if os.IsNotExist(err) {
			return SessionReplacementResult{}, &SessionImportFileNotFoundError{Path: resolved}
		}
		return SessionReplacementResult{}, err
	}
	// Importing clones the source into a new session; do not rewrite the source.
	source, err := openSessionNoRewrite(resolved, firstNonEmpty(cwdOverride, services.Cwd))
	if err != nil {
		return SessionReplacementResult{}, err
	}
	if cwdOverride != "" {
		source.Header.CWD = cwdOverride
	}
	imported, err := cloneImportedSession(source, services.SettingsManager.SessionDir())
	if err != nil {
		return SessionReplacementResult{}, err
	}
	if currentSession != nil && currentSession.shouldCancelSessionSwitch(coreext.SessionStartNew, imported.File()) {
		return SessionReplacementResult{Cancelled: true}, nil
	}
	created, err := factory(ctx, CreateAgentSessionRuntimeFactoryInput{
		Cwd:                imported.CWD(),
		AgentDir:           services.AgentDir,
		SessionManager:     imported,
		LastActiveModel:    lastModel,
		LastActiveThinking: lastThinking,
	})
	if err != nil {
		return SessionReplacementResult{}, err
	}
	return r.applyReplacement(ctx, currentSession, created, coreext.SessionShutdownNew, coreext.SessionStartNew, before, rebind, nil, SessionReplacementResult{})
}

func (r *AgentSessionRuntime) Dispose(ctx context.Context) error {
	r.mu.Lock()
	session := r.session
	before := r.beforeSessionInvalidate
	r.session = nil
	r.services = nil
	r.diagnostics = nil
	r.modelFallbackMessage = ""
	r.createRuntime = nil
	r.rebindSession = nil
	r.beforeSessionInvalidate = nil
	r.mu.Unlock()
	if session != nil {
		// Emit shutdown up front (mirroring agent-session-runtime.ts dispose),
		// then run the full session disposal so a quit/signal aborts the active
		// agent and any running bash command, not just the extension runtime.
		// disposeSession(false) avoids re-emitting session_shutdown.
		session.emitExtensionSessionShutdown(coreext.SessionShutdownQuit, "")
		if before != nil {
			before()
		}
		session.disposeSession(false)
		return nil
	}
	if before != nil {
		before()
	}
	return nil
}

func (r *AgentSessionRuntime) snapshot() (CreateAgentSessionRuntimeFactory, *AgentSession, *AgentSessionServices, func(), func(*AgentSession) error, ai.Model, ai.ThinkingLevel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createRuntime == nil {
		return nil, nil, nil, nil, nil, ai.Model{}, "", fmt.Errorf("runtime is disposed")
	}
	if r.services == nil {
		return nil, nil, nil, nil, nil, ai.Model{}, "", fmt.Errorf("runtime services are unavailable")
	}
	if r.session != nil {
		currentModel := r.session.CurrentModel()
		if currentModel.Provider != "" {
			r.lastActiveModel = currentModel
		}
		currentThinking := r.session.CurrentThinkingLevel()
		if currentThinking != "" {
			r.lastActiveThinking = currentThinking
		}
	}
	return r.createRuntime, r.session, r.services, r.beforeSessionInvalidate, r.rebindSession, r.lastActiveModel, r.lastActiveThinking, nil
}

func (r *AgentSessionRuntime) applyReplacement(
	ctx context.Context,
	previous *AgentSession,
	created CreateAgentSessionRuntimeResult,
	shutdownReason coreext.SessionShutdownReason,
	startReason coreext.SessionStartReason,
	before func(),
	rebind func(*AgentSession) error,
	withSession func(context.Context, ReplacedSessionContext) error,
	result SessionReplacementResult,
) (SessionReplacementResult, error) {
	if created.Session == nil {
		return SessionReplacementResult{}, fmt.Errorf("create runtime factory returned nil session")
	}
	if created.Services == nil {
		return SessionReplacementResult{}, fmt.Errorf("create runtime factory returned nil services")
	}
	previousSessionFile := ""
	if previous != nil && previous.Session != nil {
		previousSessionFile = previous.Session.File()
		previous.emitExtensionSessionShutdown(shutdownReason, created.Session.Session.File())
		previous.shutdownExtensionRuntime(ctx)
	}
	if before != nil {
		before()
	}
	if withSession != nil {
		if err := withSession(ctx, ReplacedSessionContext{Session: created.Session, Services: created.Services}); err != nil {
			return SessionReplacementResult{}, err
		}
	}
	r.mu.Lock()
	r.session = created.Session
	r.services = created.Services
	r.diagnostics = append([]Diagnostic(nil), created.Diagnostics...)
	r.modelFallbackMessage = created.ModelFallbackMessage
	r.lastActiveModel = created.Session.Model
	r.lastActiveThinking = created.Session.ThinkingLevel
	r.mu.Unlock()
	if rebind != nil {
		if err := rebind(created.Session); err != nil {
			return SessionReplacementResult{}, err
		}
	}
	created.Session.emitExtensionSessionStart(startReason, previousSessionFile)
	return result, nil
}

func forkSessionAtPosition(source *SessionManager, entryID string, position ForkPosition, sessionDir string) (*SessionManager, string, error) {
	if source == nil {
		return nil, "", fmt.Errorf("session is nil")
	}
	if position == "" || position == ForkPositionAt {
		target, err := CloneSessionBranch(source, entryID, sessionDir)
		return target, "", err
	}
	if position != ForkPositionBefore {
		return nil, "", fmt.Errorf("unsupported fork position: %s", position)
	}
	leafID := entryID
	if leafID == "" {
		leafID = source.CurrentLeafID()
		if leafID == "" {
			return nil, "", fmt.Errorf("nothing to clone yet")
		}
	}
	branch, err := source.BranchFrom(leafID)
	if err != nil {
		return nil, "", err
	}
	if len(branch) == 0 {
		return nil, "", fmt.Errorf("branch is empty")
	}
	selectedText := ""
	if branch[len(branch)-1].Message != nil {
		selectedText = ai.MessageText(branch[len(branch)-1].Message)
	}
	branch = branch[:len(branch)-1]
	target, err := NewSessionManager(source.CWD(), sessionDir)
	if err != nil {
		return nil, "", err
	}
	if source.File() != "" {
		target.Header.ParentSession = source.File()
	}
	target.Entries = append(target.Entries, branch...)
	retargetSessionLeaf(target)
	_, err = target.rewrite()
	return target, selectedText, err
}

func cloneImportedSession(source *SessionManager, sessionDir string) (*SessionManager, error) {
	if source == nil {
		return nil, fmt.Errorf("session is nil")
	}
	target, err := NewSessionManager(source.CWD(), sessionDir)
	if err != nil {
		return nil, err
	}
	target.Entries = append(target.Entries, source.EntriesSnapshot()...)
	retargetSessionLeaf(target)
	return target.rewrite()
}

func retargetSessionLeaf(session *SessionManager) {
	if session == nil {
		return
	}
	session.CurrentID = nil
	for i := range session.Entries {
		if session.Entries[i].ID != "" && treeEntry(session.Entries[i].Type) {
			id := session.Entries[i].ID
			session.CurrentID = &id
		}
	}
}
