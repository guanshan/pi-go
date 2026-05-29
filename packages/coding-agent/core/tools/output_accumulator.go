package tools

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// outputAccumulator collects streaming command output and produces truncated
// snapshots for display. When the output exceeds the line/byte limits the full
// output is spilled to a temp file so the caller can surface a path to the
// complete log. It mirrors src/core/tools/output-accumulator.ts (kept simpler:
// the full output is buffered in memory and written to the temp file lazily on
// truncation rather than streamed incrementally).
type outputAccumulator struct {
	prefix   string
	mu       sync.Mutex
	raw      strings.Builder
	finished bool
	tempPath string
}

func newOutputAccumulator(prefix string) *outputAccumulator {
	if prefix == "" {
		prefix = "pi-output"
	}
	return &outputAccumulator{prefix: prefix}
}

func (a *outputAccumulator) append(p []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.raw.Write(p)
}

func (a *outputAccumulator) finish() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finished = true
}

// cleanup is intentionally a no-op: the temp file (when created) is left on disk
// so the user/agent can inspect the full output via the reported path.
func (a *outputAccumulator) cleanup() {}

type outputSnapshot struct {
	content        string
	truncation     TruncationResult
	fullOutputPath string
	lastLineBytes  int
}

func (s outputSnapshot) details() any {
	if !s.truncation.Truncated && s.fullOutputPath == "" {
		return nil
	}
	d := map[string]any{}
	if s.truncation.Truncated {
		d["truncation"] = s.truncation
	}
	if s.fullOutputPath != "" {
		d["fullOutputPath"] = s.fullOutputPath
	}
	return d
}

// snapshot returns the current truncated tail plus truncation metadata. If the
// output is truncated, the full output is persisted to a temp file (once) and
// the path is recorded.
func (a *outputAccumulator) snapshot() outputSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	full := a.raw.String()
	trunc := TruncateTail(full, DefaultMaxLines, DefaultMaxBytes)
	snap := outputSnapshot{content: trunc.Content, truncation: trunc}
	if trunc.Truncated {
		if path := a.ensureTempFile(full); path != "" {
			snap.fullOutputPath = path
		}
		snap.lastLineBytes = lastLineByteLen(full)
	}
	return snap
}

func (a *outputAccumulator) ensureTempFile(full string) string {
	if a.tempPath != "" {
		return a.tempPath
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return ""
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s.log", a.prefix, hex.EncodeToString(idBytes)))
	if err := os.WriteFile(path, []byte(full), 0o600); err != nil {
		return ""
	}
	a.tempPath = path
	return path
}

func lastLineByteLen(full string) int {
	trimmed := strings.TrimSuffix(full, "\n")
	if idx := strings.LastIndexByte(trimmed, '\n'); idx != -1 {
		return len(trimmed[idx+1:])
	}
	return len(trimmed)
}

// formatBashOutput renders the final tool text, appending a truncation footer
// that points at the full-output file. Mirrors formatOutput() in bash.ts.
func formatBashOutput(snap outputSnapshot, empty string) string {
	text := snap.content
	if text == "" {
		text = empty
	}
	if !snap.truncation.Truncated {
		return text
	}
	t := snap.truncation
	startLine := t.TotalLines - t.OutputLines + 1
	endLine := t.TotalLines
	switch {
	case t.LastLinePartial:
		text += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
			FormatSize(t.OutputBytes), endLine, FormatSize(snap.lastLineBytes), snap.fullOutputPath)
	case t.TruncatedBy == "lines":
		text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
			startLine, endLine, t.TotalLines, snap.fullOutputPath)
	default:
		text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
			startLine, endLine, t.TotalLines, FormatSize(DefaultMaxBytes), snap.fullOutputPath)
	}
	return text
}
