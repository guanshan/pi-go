package tools

// imageTypeSniffBytes mirrors utils/mime.ts IMAGE_TYPE_SNIFF_BYTES: the read
// tool only inspects the first 4100 bytes of a file when sniffing for a
// supported image type.
const imageTypeSniffBytes = 4100

// pngSignature mirrors utils/mime.ts PNG_SIGNATURE.
var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// IsImagePath reports whether the given file content is a supported inline
// image, matching the TS read tool's content-sniffing behavior.
func IsImagePath(path string, data []byte) bool {
	return isImagePath(path, data)
}

// DetectMime returns the supported image MIME type for the given file content,
// or "" if the content is not a supported image.
func DetectMime(path string, data []byte) string {
	return detectMime(path, data)
}

func isImagePath(path string, data []byte) bool {
	return detectMime(path, data) != ""
}

// detectMime is a faithful port of utils/mime.ts detectSupportedImageMimeType:
// it classifies an image purely by magic bytes of the file CONTENT (never the
// extension). It rejects the JPEG variant where byte[3]==0xf7, requires a valid
// IHDR (length 13) for PNG, and rejects animated PNG (acTL chunk before IDAT).
// Only the first imageTypeSniffBytes bytes are inspected, mirroring the TS read
// path that sniffs a bounded prefix of the file.
func detectMime(_ string, data []byte) string {
	buffer := data
	if len(buffer) > imageTypeSniffBytes {
		buffer = buffer[:imageTypeSniffBytes]
	}
	return detectSupportedImageMimeType(buffer)
}

func detectSupportedImageMimeType(buffer []byte) string {
	if startsWithBytes(buffer, []byte{0xff, 0xd8, 0xff}) {
		if len(buffer) > 3 && buffer[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if startsWithBytes(buffer, pngSignature) {
		if isPNG(buffer) && !isAnimatedPNG(buffer) {
			return "image/png"
		}
		return ""
	}
	if startsWithASCII(buffer, 0, "GIF") {
		return "image/gif"
	}
	if startsWithASCII(buffer, 0, "RIFF") && startsWithASCII(buffer, 8, "WEBP") {
		return "image/webp"
	}
	return ""
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
	return int(buffer[offset])*0x1000000 + int(buffer[offset+1])<<16 + int(buffer[offset+2])<<8 + int(buffer[offset+3])
}

func startsWithBytes(buffer, prefix []byte) bool {
	if len(buffer) < len(prefix) {
		return false
	}
	for i := range prefix {
		if buffer[i] != prefix[i] {
			return false
		}
	}
	return true
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
