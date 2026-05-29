package tools

import (
	"bytes"
	"mime"
	"path/filepath"
	"strings"
)

func IsImagePath(path string, data []byte) bool {
	return isImagePath(path, data)
}

func DetectMime(path string, data []byte) string {
	return detectMime(path, data)
}

func isImagePath(path string, data []byte) bool {
	switch detectMime(path, data) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func detectMime(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	if m := mime.TypeByExtension(ext); m != "" {
		return strings.Split(m, ";")[0]
	}
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return "application/octet-stream"
}
