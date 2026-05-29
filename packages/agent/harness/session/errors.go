package session

type SessionError struct {
	Code string
	Msg  string
	Err  error
}

func (e *SessionError) Error() string {
	if e == nil {
		return ""
	}
	if e.Msg != "" {
		if e.Err != nil {
			return e.Msg + ": " + e.Err.Error()
		}
		return e.Msg
	}
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code
}

func (e *SessionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
