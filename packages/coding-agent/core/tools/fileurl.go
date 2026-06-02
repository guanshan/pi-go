package tools

import (
	"net/url"
	"path/filepath"
	"strings"
)

// FileURLToPath converts a file:// (or opaque file:) URL into a native filesystem
// path, returning false when raw is not a file URL. It is the single shared
// implementation used by the tool path resolver, the top-level NormalizePath, and
// the CLI file processor (parity review topic 8 P2-3), mirroring Node's
// fileURLToPath / TS paths.ts:74-76 including Windows drive-letter handling
// (file:///C:/x -> C:\x rather than the invalid /C:/x).
func FileURLToPath(raw string) (string, bool) {
	raw = strings.ReplaceAll(raw, "\\", "/")
	if path, ok := windowsDriveFileURLPath(raw); ok {
		return path, true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	path := u.Path
	if path == "" {
		path = u.Opaque
	}
	if u.Host != "" && u.Host != "localhost" {
		if path == "" {
			path = u.Host
		} else {
			path = "//" + u.Host + path
		}
	}
	decoded, err := url.PathUnescape(path)
	if err != nil || decoded == "" {
		return "", false
	}
	if strings.HasPrefix(decoded, "/") && isWindowsDrivePath(decoded[1:]) {
		decoded = decoded[1:]
	}
	return filepath.FromSlash(decoded), true
}

func windowsDriveFileURLPath(raw string) (string, bool) {
	const scheme = "file:"
	if !strings.HasPrefix(raw, scheme) {
		return "", false
	}
	path := strings.TrimPrefix(raw, scheme)
	if strings.HasPrefix(path, "//") {
		path = strings.TrimPrefix(path, "//")
	} else if strings.HasPrefix(path, "/") {
		path = strings.TrimLeft(path, "/")
	}
	if !isWindowsDrivePath(path) {
		return "", false
	}
	decoded, err := url.PathUnescape(path)
	if err != nil || decoded == "" {
		return "", false
	}
	return filepath.FromSlash(decoded), true
}

func isWindowsDrivePath(path string) bool {
	if len(path) < 2 || path[1] != ':' {
		return false
	}
	c := path[0]
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}
