package tools

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// This file mirrors src/core/bash-executor.ts: the shared output handling for the
// RPC/SDK bash path (AgentSession.executeBash). Unlike the interactive bash tool
// — which streams through the outputAccumulator — the RPC path buffers the full
// combined stdout+stderr and then sanitizes + truncates it in one pass. TS does
// the same logical steps per chunk: sanitizeBinaryOutput(stripAnsi(decode(...)))
// with carriage returns removed, then truncateTail, then spills the full output
// to a temp file when truncated.

// StripANSI removes ANSI escape sequences (CSI/OSC and single escapes). Mirrors
// src/utils/ansi.ts stripAnsi.
func StripANSI(value string) string {
	if !strings.ContainsRune(value, '\x1b') && !strings.ContainsRune(value, '\u009b') {
		return value
	}
	var builder strings.Builder
	for i := 0; i < len(value); {
		switch value[i] {
		case 0x1b:
			i = skipANSISequence(value, i+1)
		case 0xc2:
			if i+1 < len(value) && value[i+1] == 0x9b {
				i = skipCSI(value, i+2)
				continue
			}
			builder.WriteByte(value[i])
			i++
		default:
			builder.WriteByte(value[i])
			i++
		}
	}
	return builder.String()
}

func skipANSISequence(value string, index int) int {
	if index >= len(value) {
		return index
	}
	switch value[index] {
	case '[':
		return skipCSI(value, index+1)
	case ']':
		return skipOSC(value, index+1)
	default:
		return index + 1
	}
}

func skipCSI(value string, index int) int {
	for index < len(value) {
		b := value[index]
		index++
		if b >= 0x40 && b <= 0x7e {
			break
		}
	}
	return index
}

func skipOSC(value string, index int) int {
	for index < len(value) {
		if value[index] == 0x07 {
			return index + 1
		}
		if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '\\' {
			return index + 2
		}
		index++
	}
	return index
}

// SanitizeBinaryOutput drops control characters and invalid UTF-8 bytes while
// keeping tab/newline/carriage-return. Mirrors src/utils/shell.ts
// sanitizeBinaryOutput.
func SanitizeBinaryOutput(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r == utf8.RuneError {
			continue
		}
		if r == '\t' || r == '\n' || r == '\r' {
			builder.WriteRune(r)
			continue
		}
		if r <= 0x1f || (r >= 0xfff9 && r <= 0xfffb) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// SanitizeBashOutput applies the per-chunk transform bash-executor.ts uses:
// sanitizeBinaryOutput(stripAnsi(text)).replace(/\r/g, "").
func SanitizeBashOutput(value string) string {
	return strings.ReplaceAll(SanitizeBinaryOutput(StripANSI(value)), "\r", "")
}

// BashOutputResult is the sanitized+truncated result of a buffered bash run.
type BashOutputResult struct {
	Output         string
	Truncated      bool
	FullOutputPath string
}

// SanitizeAndTruncateBashOutput sanitizes the combined stdout+stderr, truncates
// it to the default tail limits, and (when truncated) spills the full sanitized
// output to a temp file. Mirrors the final block of executeBashWithOperations in
// bash-executor.ts: the returned Output is the truncated tail when truncated,
// otherwise the full sanitized output; FullOutputPath points at the complete log.
func SanitizeAndTruncateBashOutput(combined string) BashOutputResult {
	sanitized := SanitizeBashOutput(combined)
	trunc := TruncateTail(sanitized, DefaultMaxLines, DefaultMaxBytes)
	res := BashOutputResult{Output: sanitized, Truncated: trunc.Truncated}
	if trunc.Truncated {
		res.Output = trunc.Content
		if path := writeBashFullOutput(sanitized); path != "" {
			res.FullOutputPath = path
		}
	}
	return res
}

// writeBashFullOutput persists the full sanitized output to a uniquely-named
// temp file (left on disk so the caller can inspect it), mirroring the
// `pi-bash-<id>.log` naming in bash-executor.ts.
func writeBashFullOutput(full string) string {
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return ""
	}
	path := filepath.Join(os.TempDir(), "pi-bash-"+hex.EncodeToString(idBytes)+".log")
	if err := os.WriteFile(path, []byte(full), 0o600); err != nil {
		return ""
	}
	return path
}
