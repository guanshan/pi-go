package harnessutils

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	DefaultMaxLines   = 2000
	DefaultMaxBytes   = 50 * 1024
	GrepMaxLineLength = 500
)

type TruncatedBy string

const (
	TruncatedByLines TruncatedBy = "lines"
	TruncatedByBytes TruncatedBy = "bytes"
	TruncatedByNone  TruncatedBy = ""
)

type TruncationResult struct {
	Content               string      `json:"content"`
	Truncated             bool        `json:"truncated"`
	TruncatedBy           TruncatedBy `json:"truncatedBy,omitempty"`
	TotalLines            int         `json:"totalLines"`
	TotalBytes            int         `json:"totalBytes"`
	OutputLines           int         `json:"outputLines"`
	OutputBytes           int         `json:"outputBytes"`
	LastLinePartial       bool        `json:"lastLinePartial"`
	FirstLineExceedsLimit bool        `json:"firstLineExceedsLimit"`
	MaxLines              int         `json:"maxLines"`
	MaxBytes              int         `json:"maxBytes"`
}

type TruncationOptions struct {
	MaxLines int
	MaxBytes int
}

func FormatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

func TruncateHead(content string, opts TruncationOptions) TruncationResult {
	maxLines, maxBytes := truncationLimits(opts)
	totalBytes := len([]byte(content))
	lines := strings.Split(content, "\n")
	totalLines := len(lines)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return noTruncation(content, totalLines, totalBytes, maxLines, maxBytes)
	}
	if len(lines) > 0 && len([]byte(lines[0])) > maxBytes {
		return TruncationResult{
			Truncated:             true,
			TruncatedBy:           TruncatedByBytes,
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}
	var out []string
	outputBytes := 0
	truncatedBy := TruncatedByLines
	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := len([]byte(lines[i]))
		if i > 0 {
			lineBytes++
		}
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = TruncatedByBytes
			break
		}
		out = append(out, lines[i])
		outputBytes += lineBytes
	}
	contentOut := strings.Join(out, "\n")
	return TruncationResult{
		Content:     contentOut,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(out),
		OutputBytes: len([]byte(contentOut)),
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}

func TruncateTail(content string, opts TruncationOptions) TruncationResult {
	maxLines, maxBytes := truncationLimits(opts)
	totalBytes := len([]byte(content))
	lines := strings.Split(content, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	totalLines := len(lines)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return noTruncation(content, totalLines, totalBytes, maxLines, maxBytes)
	}
	out := []string{}
	outputBytes := 0
	truncatedBy := TruncatedByLines
	lastLinePartial := false
	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		lineBytes := len([]byte(lines[i]))
		if len(out) > 0 {
			lineBytes++
		}
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = TruncatedByBytes
			if len(out) == 0 {
				truncated := truncateStringToBytesFromEnd(lines[i], maxBytes)
				out = append([]string{truncated}, out...)
				lastLinePartial = true
			}
			break
		}
		out = append([]string{lines[i]}, out...)
		outputBytes += lineBytes
	}
	contentOut := strings.Join(out, "\n")
	return TruncationResult{
		Content:         contentOut,
		Truncated:       true,
		TruncatedBy:     truncatedBy,
		TotalLines:      totalLines,
		TotalBytes:      totalBytes,
		OutputLines:     len(out),
		OutputBytes:     len([]byte(contentOut)),
		LastLinePartial: lastLinePartial,
		MaxLines:        maxLines,
		MaxBytes:        maxBytes,
	}
}

func TruncateGrepLine(line string) string {
	truncated, ok := TruncateLine(line, GrepMaxLineLength)
	if !ok {
		return line
	}
	return truncated
}

func TruncateLine(line string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = GrepMaxLineLength
	}
	if len(line) <= maxChars {
		return line, false
	}
	return line[:maxChars] + "... [truncated]", true
}

func truncationLimits(opts TruncationOptions) (int, int) {
	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return maxLines, maxBytes
}

func noTruncation(content string, totalLines, totalBytes, maxLines, maxBytes int) TruncationResult {
	return TruncationResult{
		Content:     content,
		Truncated:   false,
		TruncatedBy: TruncatedByNone,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: totalLines,
		OutputBytes: totalBytes,
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}

func truncateStringToBytesFromEnd(str string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	runes := []rune(str)
	outputBytes := 0
	start := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		runeBytes := utf8.RuneLen(runes[i])
		if runeBytes < 0 {
			runeBytes = len([]byte(string(runes[i])))
		}
		if outputBytes+runeBytes > maxBytes {
			break
		}
		outputBytes += runeBytes
		start = i
	}
	return string(runes[start:])
}
