package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ErrSessionSelectionCancelled is returned when the user cancels --resume selection.
var ErrSessionSelectionCancelled = errors.New("session selection cancelled")

// SessionChoice is the CLI package's presentation copy of a persisted session.
type SessionChoice struct {
	ID        string
	Path      string
	Name      string
	Preview   string
	UpdatedAt time.Time
}

// SelectSession prints a numbered selector and returns the selected session path.
func SelectSession(r io.Reader, w io.Writer, sessions []SessionChoice) (string, error) {
	if len(sessions) == 0 {
		return "", errors.New("no sessions found")
	}
	if len(sessions) == 1 {
		return sessions[0].Path, nil
	}
	for i, session := range sessions {
		label := firstNonEmpty(session.Name, session.Preview)
		fmt.Fprintf(w, "%d. %s %s %s\n", i+1, session.ID, session.UpdatedAt.Format("2006-01-02 15:04"), label)
	}
	fmt.Fprintf(w, "Select session [1-%d] (Enter for 1, q to cancel): ", len(sessions))

	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		return sessions[0].Path, nil
	}
	if strings.EqualFold(choice, "q") || strings.EqualFold(choice, "quit") || strings.EqualFold(choice, "cancel") {
		return "", ErrSessionSelectionCancelled
	}
	index, err := strconv.Atoi(choice)
	if err != nil || index < 1 || index > len(sessions) {
		return "", fmt.Errorf("invalid session selection %q", choice)
	}
	return sessions[index-1].Path, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
