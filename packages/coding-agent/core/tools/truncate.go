package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func TruncateHead(content string, maxLines, maxBytes int) TruncationResult {
	return truncate(content, maxLines, maxBytes, false)
}

func TruncateTail(content string, maxLines, maxBytes int) TruncationResult {
	return truncate(content, maxLines, maxBytes, true)
}

func truncate(content string, maxLines, maxBytes int, tail bool) TruncationResult {
	totalBytes := len([]byte(content))
	lines := splitLinesForCounting(content)
	totalLines := len(lines)
	res := TruncationResult{
		Content: content, TotalLines: totalLines, TotalBytes: totalBytes,
		OutputLines: totalLines, OutputBytes: totalBytes, MaxLines: maxLines, MaxBytes: maxBytes,
	}
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return res
	}
	res.Truncated = true
	if !tail && len(lines) > 0 && len([]byte(lines[0])) > maxBytes {
		res.TruncatedBy = "bytes"
		res.Content = ""
		res.OutputLines = 0
		res.OutputBytes = 0
		res.FirstLineExceedsLimit = true
		return res
	}
	if tail {
		var out []string
		bytesCount := 0
		for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
			lineBytes := len([]byte(lines[i]))
			if len(out) > 0 {
				lineBytes++
			}
			if bytesCount+lineBytes > maxBytes {
				res.TruncatedBy = "bytes"
				if len(out) == 0 {
					part := truncateStringFromEnd(lines[i], maxBytes)
					out = append([]string{part}, out...)
					res.LastLinePartial = true
				}
				break
			}
			out = append([]string{lines[i]}, out...)
			bytesCount += lineBytes
		}
		if res.TruncatedBy == "" {
			res.TruncatedBy = "lines"
		}
		res.Content = strings.Join(out, "\n")
		res.OutputLines = len(out)
		res.OutputBytes = len([]byte(res.Content))
		return res
	}
	var out []string
	bytesCount := 0
	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := len([]byte(lines[i]))
		if i > 0 {
			lineBytes++
		}
		if bytesCount+lineBytes > maxBytes {
			res.TruncatedBy = "bytes"
			break
		}
		out = append(out, lines[i])
		bytesCount += lineBytes
	}
	if res.TruncatedBy == "" {
		res.TruncatedBy = "lines"
	}
	res.Content = strings.Join(out, "\n")
	res.OutputLines = len(out)
	res.OutputBytes = len([]byte(res.Content))
	return res
}

func splitLinesForCounting(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func truncateStringFromEnd(s string, maxBytes int) string {
	if len([]byte(s)) <= maxBytes {
		return s
	}
	for len([]byte(s)) > maxBytes {
		_, size := utf8.DecodeRuneInString(s)
		if size <= 0 {
			return ""
		}
		s = s[size:]
	}
	return s
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
