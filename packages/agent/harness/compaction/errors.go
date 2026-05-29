package compaction

type CompactionError struct {
	Code string
	Msg  string
	Err  error
}

func (e *CompactionError) Error() string {
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

func (e *CompactionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type BranchSummaryError struct {
	Code string
	Msg  string
	Err  error
}

func (e *BranchSummaryError) Error() string {
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

func (e *BranchSummaryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
