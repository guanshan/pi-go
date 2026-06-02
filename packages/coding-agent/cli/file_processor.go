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
		data, err := os.ReadFile(path)
		if err != nil {
			return ProcessedFiles{}, err
		}
		if len(data) == 0 {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		abs = filepath.Clean(abs)
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
	if strings.HasPrefix(path, "file:") {
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

func detectSupportedImageMime(data []byte) string {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff && (len(data) <= 3 || data[3] != 0xf7) {
		return "image/jpeg"
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
