package agent

import "errors"

type AgentErrorCode string

const (
	AgentErrBusy            AgentErrorCode = "busy"
	AgentErrInvalidState    AgentErrorCode = "invalid_state"
	AgentErrInvalidArgument AgentErrorCode = "invalid_argument"
	AgentErrHook            AgentErrorCode = "hook"
	AgentErrSession         AgentErrorCode = "session"
	AgentErrAuth            AgentErrorCode = "auth"
	AgentErrCompaction      AgentErrorCode = "compaction"
	AgentErrBranchSummary   AgentErrorCode = "branch_summary"
	AgentErrUnknown         AgentErrorCode = "unknown"
)

func (c AgentErrorCode) String() string {
	if c == "" {
		return string(AgentErrUnknown)
	}
	return string(c)
}

type AgentError struct {
	Code AgentErrorCode
	Msg  string
	Err  error
}

func (e *AgentError) Error() string {
	if e == nil {
		return ""
	}
	msg := e.Msg
	if msg == "" {
		msg = "agent error"
	}
	if e.Err != nil {
		return e.Code.String() + ": " + msg + ": " + e.Err.Error()
	}
	return e.Code.String() + ": " + msg
}

func (e *AgentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func agentError(code AgentErrorCode, msg string, err error) error {
	if err == nil {
		return &AgentError{Code: code, Msg: msg}
	}
	var existing *AgentError
	if AsAgentError(err, &existing) {
		return err
	}
	return &AgentError{Code: code, Msg: msg, Err: err}
}

func AsAgentError(err error, target **AgentError) bool {
	return errors.As(err, target)
}
