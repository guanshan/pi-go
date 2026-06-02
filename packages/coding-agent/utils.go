package codingagent

import (
	"context"
	"errors"
	"html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

const imageTypeSniffBytes = 4100

var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

type ParsedFrontmatter struct {
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
}

func ParseFrontmatter(content string) (ParsedFrontmatter, error) {
	yamlString, body := extractFrontmatter(content)
	if yamlString == "" {
		return ParsedFrontmatter{Frontmatter: map[string]any{}, Body: body}, nil
	}
	frontmatter, err := parseSimpleYAML(yamlString)
	if err != nil {
		return ParsedFrontmatter{}, err
	}
	return ParsedFrontmatter{Frontmatter: frontmatter, Body: body}, nil
}

func StripFrontmatter(content string) string {
	parsed, err := ParseFrontmatter(content)
	if err != nil {
		return normalizeNewlines(content)
	}
	return parsed.Body
}

func DetectSupportedImageMimeType(buffer []byte) string {
	switch http.DetectContentType(buffer) {
	case "image/jpeg":
		if len(buffer) > 3 && buffer[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	case "image/png":
		if !isPNG(buffer) || isAnimatedPNG(buffer) {
			return ""
		}
		return "image/png"
	case "image/gif":
		return "image/gif"
	}
	if startsWithASCII(buffer, 0, "RIFF") && startsWithASCII(buffer, 8, "WEBP") {
		return "image/webp"
	}
	return ""
}

func DetectSupportedImageMimeTypeFromFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	buffer := make([]byte, imageTypeSniffBytes)
	n, err := file.Read(buffer)
	if err != nil && n == 0 {
		return "", err
	}
	return DetectSupportedImageMimeType(buffer[:n]), nil
}

type DecodedHTMLEntity struct {
	Text   string `json:"text"`
	Length int    `json:"length"`
}

func DecodeHTMLEntity(entity string) string {
	if strings.HasPrefix(entity, "#x") || strings.HasPrefix(entity, "#X") {
		code, err := strconv.ParseInt(entity[2:], 16, 32)
		if err != nil || !utf8.ValidRune(rune(code)) {
			return ""
		}
		return string(rune(code))
	}
	if strings.HasPrefix(entity, "#") {
		code, err := strconv.ParseInt(entity[1:], 10, 32)
		if err != nil || !utf8.ValidRune(rune(code)) {
			return ""
		}
		return string(rune(code))
	}
	encoded := "&" + entity + ";"
	decoded := html.UnescapeString(encoded)
	if decoded == encoded {
		return ""
	}
	return decoded
}

func DecodeHTMLEntityAt(input string, index int) (DecodedHTMLEntity, bool) {
	if index < 0 || index >= len(input) || input[index] != '&' {
		return DecodedHTMLEntity{}, false
	}
	semicolon := strings.IndexByte(input[index+1:], ';')
	if semicolon < 0 || semicolon+1 > 16 {
		return DecodedHTMLEntity{}, false
	}
	length := semicolon + 2
	decoded := DecodeHTMLEntity(input[index+1 : index+length-1])
	if decoded == "" {
		return DecodedHTMLEntity{}, false
	}
	return DecodedHTMLEntity{Text: decoded, Length: length}, true
}

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

type ShellConfig struct {
	Shell string   `json:"shell"`
	Args  []string `json:"args"`
}

func GetShellConfig(customShellPath ...string) (ShellConfig, error) {
	if len(customShellPath) > 0 && customShellPath[0] != "" {
		if _, err := os.Stat(customShellPath[0]); err == nil {
			return ShellConfig{Shell: customShellPath[0], Args: []string{"-c"}}, nil
		}
		return ShellConfig{}, errors.New("custom shell path not found: " + customShellPath[0])
	}
	if runtime.GOOS == "windows" {
		candidates := []string{}
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			candidates = append(candidates, filepath.Join(programFiles, "Git", "bin", "bash.exe"))
		}
		if programFiles := os.Getenv("ProgramFiles(x86)"); programFiles != "" {
			candidates = append(candidates, filepath.Join(programFiles, "Git", "bin", "bash.exe"))
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				return ShellConfig{Shell: candidate, Args: []string{"-c"}}, nil
			}
		}
		if bash, err := exec.LookPath("bash.exe"); err == nil {
			return ShellConfig{Shell: bash, Args: []string{"-c"}}, nil
		}
		return ShellConfig{}, errors.New("no bash shell found")
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return ShellConfig{Shell: "/bin/bash", Args: []string{"-c"}}, nil
	}
	if bash, err := exec.LookPath("bash"); err == nil {
		return ShellConfig{Shell: bash, Args: []string{"-c"}}, nil
	}
	return ShellConfig{Shell: "sh", Args: []string{"-c"}}, nil
}

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

type PathInputOptions struct {
	Trim                   bool
	ExpandTilde            *bool
	HomeDir                string
	StripAtPrefix          bool
	NormalizeUnicodeSpaces bool
}

func CanonicalizePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

func IsLocalPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	for _, prefix := range []string{"npm:", "git:", "github:", "http:", "https:", "ssh:"} {
		if strings.HasPrefix(trimmed, prefix) {
			return false
		}
	}
	return true
}

func NormalizePath(input string, options PathInputOptions) string {
	normalized := input
	if options.Trim {
		normalized = strings.TrimSpace(normalized)
	}
	if options.NormalizeUnicodeSpaces {
		normalized = normalizeUnicodeSpaces(normalized)
	}
	if options.StripAtPrefix && strings.HasPrefix(normalized, "@") {
		normalized = normalized[1:]
	}
	expandTilde := true
	if options.ExpandTilde != nil {
		expandTilde = *options.ExpandTilde
	}
	if expandTilde {
		home := options.HomeDir
		if home == "" {
			if h, err := os.UserHomeDir(); err == nil {
				home = h
			}
		}
		if normalized == "~" {
			return home
		}
		if strings.HasPrefix(normalized, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(normalized, `~\`)) {
			return filepath.Join(home, normalized[2:])
		}
	}
	if strings.HasPrefix(normalized, "file:") {
		if path, ok := catools.FileURLToPath(normalized); ok {
			return path
		}
	}
	return normalized
}

func ResolveInputPath(input, baseDir string, options PathInputOptions) string {
	normalized := NormalizePath(input, options)
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	baseDir = NormalizePath(baseDir, PathInputOptions{})
	if filepath.IsAbs(normalized) {
		return filepath.Clean(normalized)
	}
	return filepath.Clean(filepath.Join(baseDir, normalized))
}

func GetCWDRelativePath(filePath, cwd string) (string, bool) {
	resolvedCWD := ResolveInputPath(cwd, "", PathInputOptions{})
	resolvedPath := ResolveInputPath(filePath, resolvedCWD, PathInputOptions{})
	relative, err := filepath.Rel(resolvedCWD, resolvedPath)
	if err != nil {
		return "", false
	}
	if relative == "." {
		return ".", true
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", false
	}
	return relative, true
}

func FormatPathRelativeToCWDOrAbsolute(filePath, cwd string) string {
	absolute := ResolveInputPath(filePath, cwd, PathInputOptions{})
	if relative, ok := GetCWDRelativePath(absolute, cwd); ok {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(absolute)
}

func MarkPathIgnoredByCloudSync(path string) {
	var commands [][]string
	switch runtime.GOOS {
	case "darwin":
		commands = [][]string{{"xattr", "-w", "com.dropbox.ignored", "1", path}, {"xattr", "-w", "com.apple.fileprovider.ignore#P", "1", path}}
	case "linux":
		commands = [][]string{{"setfattr", "-n", "user.com.dropbox.ignored", "-v", "1", path}}
	}
	for _, command := range commands {
		_ = exec.Command(command[0], command[1:]...).Run()
	}
}

func Sleep(ctx context.Context, duration time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errors.New("Aborted")
	case <-timer.C:
		return nil
	}
}

func extractFrontmatter(content string) (string, string) {
	normalized := normalizeNewlines(content)
	if !strings.HasPrefix(normalized, "---") {
		return "", normalized
	}
	endIndex := strings.Index(normalized[3:], "\n---")
	if endIndex == -1 {
		return "", normalized
	}
	endIndex += 3
	return normalized[4:endIndex], strings.TrimSpace(normalized[endIndex+4:])
}

func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func parseSimpleYAML(input string) (map[string]any, error) {
	out := map[string]any{}
	for _, line := range strings.Split(input, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, errors.New("invalid frontmatter line: " + line)
		}
		out[strings.TrimSpace(key)] = parseYAMLScalar(strings.TrimSpace(value))
	}
	return out, nil
}

func parseYAMLScalar(value string) any {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return strings.Trim(value, `"'`)
		}
	}
	switch strings.ToLower(value) {
	case "true":
		return true
	case "false":
		return false
	case "null", "~":
		return nil
	}
	if i, err := strconv.Atoi(value); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		inner := strings.TrimSpace(value[1 : len(value)-1])
		if inner == "" {
			return []any{}
		}
		parts := strings.Split(inner, ",")
		items := make([]any, 0, len(parts))
		for _, part := range parts {
			items = append(items, parseYAMLScalar(strings.TrimSpace(part)))
		}
		return items
	}
	return value
}

func startsWithASCII(buffer []byte, offset int, text string) bool {
	if len(buffer) < offset+len(text) {
		return false
	}
	for i := range text {
		if buffer[offset+i] != text[i] {
			return false
		}
	}
	return true
}

func isPNG(buffer []byte) bool {
	return len(buffer) >= 16 && readUint32BE(buffer, len(pngSignature)) == 13 && startsWithASCII(buffer, 12, "IHDR")
}

func isAnimatedPNG(buffer []byte) bool {
	offset := len(pngSignature)
	for offset+8 <= len(buffer) {
		chunkLength := readUint32BE(buffer, offset)
		chunkTypeOffset := offset + 4
		if startsWithASCII(buffer, chunkTypeOffset, "acTL") {
			return true
		}
		if startsWithASCII(buffer, chunkTypeOffset, "IDAT") {
			return false
		}
		nextOffset := offset + 8 + chunkLength + 4
		if nextOffset <= offset || nextOffset > len(buffer) {
			return false
		}
		offset = nextOffset
	}
	return false
}

func readUint32BE(buffer []byte, offset int) int {
	if len(buffer) < offset+4 {
		return 0
	}
	return int(buffer[offset])<<24 | int(buffer[offset+1])<<16 | int(buffer[offset+2])<<8 | int(buffer[offset+3])
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

func normalizeUnicodeSpaces(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == 0x00a0 || r == 0x202f || r == 0x205f || r == 0x3000:
			return ' '
		case r >= 0x2000 && r <= 0x200a:
			return ' '
		default:
			return r
		}
	}, value)
}

func EscapeHTML(value string) string {
	return html.EscapeString(value)
}
