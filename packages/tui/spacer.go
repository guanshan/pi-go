package tui

type Spacer struct {
	Height int
}

func (s *Spacer) Render(width int) []string {
	if s.Height <= 0 {
		return nil
	}
	return make([]string, s.Height)
}
