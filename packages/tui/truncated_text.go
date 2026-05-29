package tui

type TruncatedText struct {
	Text string
}

func (t *TruncatedText) Render(width int) []string {
	return []string{TruncateToWidth(t.Text, width, "...")}
}
