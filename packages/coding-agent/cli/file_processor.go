package cli

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/ai/imageresize"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
	"golang.org/x/text/unicode/norm"
)

type ProcessedFiles struct {
	Text   string
	Images []ai.ContentBlock
}

type ProcessFileOptions struct {
	AutoResizeImages bool
}

var cliMacOSScreenshotAMPMPattern = regexp.MustCompile(` (?i:am|pm)\.`)

func ProcessFileArguments(cwd string, fileArgs []string, options ...ProcessFileOptions) (ProcessedFiles, error) {
	// Default to resizing images, matching the TypeScript processFileArguments
	// default of autoResizeImages = true.
	autoResize := true
	if len(options) > 0 {
		autoResize = options[0].AutoResizeImages
	}
	var result ProcessedFiles
	for _, fileArg := range fileArgs {
		path := ResolveReadPath(cwd, fileArg)
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		abs = filepath.Clean(abs)
		data, err := os.ReadFile(path)
		if err != nil {
			// Mirror file-processor.ts wording: a missing file reports "File not
			// found: <path>" (the access() check at file-processor.ts:37); any
			// other read failure reports "Could not read file <path>: <message>"
			// with the colon AFTER the path and the underlying error appended
			// (file-processor.ts:92). The absolute, cleaned path is used (matching
			// TS resolve()).
			if os.IsNotExist(err) {
				return ProcessedFiles{}, fmt.Errorf("File not found: %s", abs) //nolint:staticcheck // TS-compatible user-facing message.
			}
			return ProcessedFiles{}, fmt.Errorf("Could not read file %s: %s", abs, err) //nolint:staticcheck // TS-compatible user-facing message.
		}
		if len(data) == 0 {
			continue
		}
		if mimeType := detectSupportedImageMime(data); mimeType != "" {
			if !autoResize {
				result.Images = append(result.Images, ai.ContentBlock{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: mimeType})
				result.Text += fmt.Sprintf("<file name=\"%s\"></file>\n", abs)
				continue
			}
			resized := imageresize.Resize(data, mimeType, imageresize.Options{})
			if resized == nil {
				result.Text += fmt.Sprintf("<file name=\"%s\">[Image omitted: could not be resized below the inline image size limit.]</file>\n", abs)
				continue
			}
			result.Images = append(result.Images, ai.ContentBlock{Type: "image", Data: resized.Data, MimeType: resized.MimeType})
			if note := imageresize.DimensionNote(resized); note != "" {
				result.Text += fmt.Sprintf("<file name=\"%s\">%s</file>\n", abs, note)
			} else {
				result.Text += fmt.Sprintf("<file name=\"%s\"></file>\n", abs)
			}
			continue
		}
		result.Text += fmt.Sprintf("<file name=\"%s\">\n%s\n</file>\n", abs, string(data))
	}
	return result, nil
}

func ResolveReadPath(cwd, path string) string {
	resolved := ResolvePath(cwd, normalizePathInput(path, true))
	if fileExists(resolved) {
		return resolved
	}
	for _, candidate := range readPathVariants(resolved) {
		if candidate != resolved && fileExists(candidate) {
			return candidate
		}
	}
	return resolved
}

func ResolvePath(cwd, path string) string {
	path = normalizePathInput(path, false)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

func readPathVariants(path string) []string {
	amPM := cliMacOSScreenshotAMPMPattern.ReplaceAllStringFunc(path, func(match string) string {
		return "\u202f" + match[1:]
	})
	nfd := norm.NFD.String(path)
	curly := strings.ReplaceAll(path, "'", "\u2019")
	return []string{amPM, nfd, curly, strings.ReplaceAll(nfd, "'", "\u2019")}
}

func normalizePathInput(path string, stripAtPrefix bool) string {
	if stripAtPrefix && strings.HasPrefix(path, "@") {
		path = strings.TrimPrefix(path, "@")
	}
	path = strings.Map(func(r rune) rune {
		switch {
		case r == '\u00a0' || r == '\u202f' || r == '\u205f' || r == '\u3000':
			return ' '
		case r >= '\u2000' && r <= '\u200a':
			return ' '
		default:
			return r
		}
	}, path)
	// Only a genuine `file://` URL is decoded (TS paths.ts: /^file:\/\//). A bare
	// `file:foo` is treated as a plain relative path, not a URL.
	if strings.HasPrefix(path, "file://") {
		if decoded, ok := fileURLPath(path); ok {
			path = decoded
		}
	}
	path = expandTilde(path)
	return strings.TrimFunc(path, unicode.IsControl)
}

// fileURLPath delegates to the shared core/tools implementation so the tool path
// resolver (tools/path.go), the top-level NormalizePath, and this CLI processor
// all use a single file:// parser (parity review topic 8 P2-3).
func fileURLPath(raw string) (string, bool) {
	return catools.FileURLToPath(raw)
}

func expandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// pngSignature is the 8-byte PNG magic, matching utils/mime.ts PNG_SIGNATURE.
var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// detectSupportedImageMime mirrors utils/mime.ts detectSupportedImageMimeType:
// header sniffing with PNG validated via IHDR (and animated PNG rejected), and
// GIF matched on the 3-byte "GIF" prefix only.
func detectSupportedImageMime(data []byte) string {
	// JPEG: ff d8 ff, but ff d8 ff f7 (JPEG-LS) is rejected. A buffer of exactly
	// 3 bytes (no data[3]) is still treated as JPEG, matching TS's undefined !==
	// 0xf7 comparison.
	if startsWith(data, []byte{0xff, 0xd8, 0xff}) {
		if len(data) > 3 && data[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if startsWith(data, pngSignature) {
		if isPng(data) && !isAnimatedPng(data) {
			return "image/png"
		}
		return ""
	}
	if startsWithASCII(data, 0, "GIF") {
		return "image/gif"
	}
	if startsWithASCII(data, 0, "RIFF") && startsWithASCII(data, 8, "WEBP") {
		return "image/webp"
	}
	return ""
}

// isPng requires a valid IHDR chunk (length 13) immediately after the signature,
// matching utils/mime.ts isPng().
func isPng(data []byte) bool {
	return len(data) >= 16 && readUint32BE(data, len(pngSignature)) == 13 && startsWithASCII(data, 12, "IHDR")
}

// isAnimatedPng walks PNG chunks looking for an acTL chunk before the first
// IDAT, matching utils/mime.ts isAnimatedPng().
func isAnimatedPng(data []byte) bool {
	offset := len(pngSignature)
	for offset+8 <= len(data) {
		chunkLength := readUint32BE(data, offset)
		chunkTypeOffset := offset + 4
		if startsWithASCII(data, chunkTypeOffset, "acTL") {
			return true
		}
		if startsWithASCII(data, chunkTypeOffset, "IDAT") {
			return false
		}
		nextOffset := offset + 8 + chunkLength + 4
		if nextOffset <= offset || nextOffset > len(data) {
			return false
		}
		offset = nextOffset
	}
	return false
}

// readUint32BE reads a big-endian uint32 at offset, returning 0 for any byte
// past the end of the buffer (matching the `?? 0` reads in utils/mime.ts).
func readUint32BE(data []byte, offset int) int {
	return int(byteAt(data, offset))<<24 |
		int(byteAt(data, offset+1))<<16 |
		int(byteAt(data, offset+2))<<8 |
		int(byteAt(data, offset+3))
}

func byteAt(data []byte, offset int) byte {
	if offset < 0 || offset >= len(data) {
		return 0
	}
	return data[offset]
}

func startsWith(data, prefix []byte) bool {
	if len(data) < len(prefix) {
		return false
	}
	return bytes.Equal(data[:len(prefix)], prefix)
}

func startsWithASCII(data []byte, offset int, text string) bool {
	if len(data) < offset+len(text) {
		return false
	}
	for i := 0; i < len(text); i++ {
		if data[offset+i] != text[i] {
			return false
		}
	}
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
