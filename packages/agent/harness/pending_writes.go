package harness

import (
	"context"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
)

type pendingSessionWrite interface {
	apply(context.Context, *session.Session) (string, error)
}

type pendingMessageWrite struct {
	Message agent.AgentMessage
}

func (w pendingMessageWrite) apply(ctx context.Context, sess *session.Session) (string, error) {
	return sess.AppendMessage(ctx, w.Message)
}

type pendingModelChangeWrite struct {
	Provider string
	ModelID  string
}

func (w pendingModelChangeWrite) apply(ctx context.Context, sess *session.Session) (string, error) {
	return sess.AppendModelChange(ctx, w.Provider, w.ModelID)
}

type pendingThinkingLevelChangeWrite struct {
	ThinkingLevel string
}

func (w pendingThinkingLevelChangeWrite) apply(ctx context.Context, sess *session.Session) (string, error) {
	return sess.AppendThinkingLevelChange(ctx, w.ThinkingLevel)
}

type pendingActiveToolsChangeWrite struct {
	ActiveToolNames []string
}

func (w pendingActiveToolsChangeWrite) apply(ctx context.Context, sess *session.Session) (string, error) {
	return sess.AppendActiveToolsChange(ctx, append([]string(nil), w.ActiveToolNames...))
}

type pendingCustomWrite struct {
	CustomType string
	Data       any
}

func (w pendingCustomWrite) apply(ctx context.Context, sess *session.Session) (string, error) {
	return sess.AppendCustomEntry(ctx, w.CustomType, w.Data)
}

type pendingCustomMessageWrite struct {
	CustomType string
	Content    any
	Display    bool
	Details    any
}

func (w pendingCustomMessageWrite) apply(ctx context.Context, sess *session.Session) (string, error) {
	return sess.AppendCustomMessageEntry(ctx, w.CustomType, w.Content, w.Display, w.Details)
}

type pendingLabelWrite struct {
	TargetID string
	Label    string
}

func (w pendingLabelWrite) apply(ctx context.Context, sess *session.Session) (string, error) {
	return sess.AppendLabel(ctx, w.TargetID, w.Label)
}

type pendingSessionInfoWrite struct {
	Name string
}

func (w pendingSessionInfoWrite) apply(ctx context.Context, sess *session.Session) (string, error) {
	return sess.AppendSessionName(ctx, w.Name)
}

type pendingLeafWrite struct {
	TargetID *string
}

func (w pendingLeafWrite) apply(ctx context.Context, sess *session.Session) (string, error) {
	if err := sess.Storage().SetLeafID(ctx, cloneStringPtr(w.TargetID)); err != nil {
		return "", err
	}
	leaf, err := sess.LeafID(ctx)
	if err != nil || leaf == nil {
		return "", err
	}
	return *leaf, nil
}
