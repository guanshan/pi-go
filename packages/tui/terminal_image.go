package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ImageProtocol string

const (
	ImageProtocolNone   ImageProtocol = ""
	ImageProtocolKitty  ImageProtocol = "kitty"
	ImageProtocolITerm2 ImageProtocol = "iterm2"
)

type ImageDimensions struct {
	Width  int
	Height int
}

type CellDimensions struct {
	Width  int
	Height int
}

type ImageCellSize struct {
	Columns int
	Rows    int
}

type TerminalCapabilities struct {
	Images     ImageProtocol `json:"images,omitempty"`
	TrueColor  bool          `json:"trueColor"`
	Hyperlinks bool          `json:"hyperlinks"`
	Kitty      bool          `json:"kitty,omitempty"`
	ITerm2     bool          `json:"iterm2,omitempty"`
	Sixel      bool          `json:"sixel,omitempty"`
}

var (
	capsMu             sync.RWMutex
	capabilities       = TerminalCapabilities{}
	capabilitiesCached bool
	cellDimsMu         sync.RWMutex
	cellDims           = CellDimensions{Width: 9, Height: 18}
	nextImageID        atomic.Int64
)

func init() { nextImageID.Store(1) }

func SetCapabilities(c TerminalCapabilities) {
	capsMu.Lock()
	defer capsMu.Unlock()
	capabilities = normalizeCapabilities(c)
	capabilitiesCached = true
}

func GetCapabilities() TerminalCapabilities {
	capsMu.RLock()
	if capabilitiesCached {
		c := capabilities
		capsMu.RUnlock()
		return c
	}
	capsMu.RUnlock()
	capsMu.Lock()
	defer capsMu.Unlock()
	if !capabilitiesCached {
		capabilities = DetectCapabilities()
		capabilitiesCached = true
	}
	return capabilities
}

func ResetCapabilitiesCache() {
	capsMu.Lock()
	defer capsMu.Unlock()
	capabilities = TerminalCapabilities{}
	capabilitiesCached = false
}

func SetCellDimensions(c CellDimensions) {
	cellDimsMu.Lock()
	cellDims = c
	cellDimsMu.Unlock()
}

func GetCellDimensions() CellDimensions {
	cellDimsMu.RLock()
	defer cellDimsMu.RUnlock()
	return cellDims
}

// cellDimsSnapshot returns the cell dimensions for use inside size
// calculations without taking the lock for every field read.
func cellDimsSnapshot() CellDimensions { return GetCellDimensions() }

func DetectCapabilities() TerminalCapabilities {
	env := map[string]string{}
	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			env[key] = value
		}
	}
	return DetectCapabilitiesFromEnv(env)
}

func DetectCapabilitiesFromEnv(env map[string]string) TerminalCapabilities {
	return DetectCapabilitiesFromEnvWithTmuxProbe(env, probeTmuxHyperlinks)
}

func DetectCapabilitiesFromEnvWithTmuxProbe(env map[string]string, tmuxForwardsHyperlinks func() bool) TerminalCapabilities {
	termProgram := strings.ToLower(env["TERM_PROGRAM"])
	terminalEmulator := strings.ToLower(env["TERMINAL_EMULATOR"])
	term := strings.ToLower(env["TERM"])
	colorTerm := strings.ToLower(env["COLORTERM"])
	hasTrueColorHint := colorTerm == "truecolor" || colorTerm == "24bit"
	inTmux := env["TMUX"] != "" || strings.HasPrefix(term, "tmux")
	if inTmux {
		hyperlinks := false
		if tmuxForwardsHyperlinks != nil {
			hyperlinks = tmuxForwardsHyperlinks()
		}
		return normalizeCapabilities(TerminalCapabilities{TrueColor: hasTrueColorHint, Hyperlinks: hyperlinks})
	}
	if strings.HasPrefix(term, "screen") {
		return normalizeCapabilities(TerminalCapabilities{TrueColor: hasTrueColorHint, Hyperlinks: false})
	}
	if env["KITTY_WINDOW_ID"] != "" || termProgram == "kitty" {
		return normalizeCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	}
	if termProgram == "ghostty" || strings.Contains(term, "ghostty") || env["GHOSTTY_RESOURCES_DIR"] != "" {
		return normalizeCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	}
	if env["WEZTERM_PANE"] != "" || termProgram == "wezterm" {
		return normalizeCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	}
	if env["ITERM_SESSION_ID"] != "" || termProgram == "iterm.app" {
		return normalizeCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true})
	}
	if env["WT_SESSION"] != "" {
		return normalizeCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: true})
	}
	switch termProgram {
	case "vscode", "alacritty":
		return normalizeCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: true})
	case "apple_terminal":
		// Apple Terminal supports 256 colors but no truecolor and no OSC 8.
		return normalizeCapabilities(TerminalCapabilities{TrueColor: false, Hyperlinks: false})
	case "hyper":
		// Hyper supports truecolor and OSC 8.
		return normalizeCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: true})
	}
	if terminalEmulator == "jetbrains-jediterm" {
		return normalizeCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: false})
	}
	if env["KONSOLE_VERSION"] != "" {
		// Konsole has truecolor and OSC 8.
		return normalizeCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: true})
	}
	return normalizeCapabilities(TerminalCapabilities{TrueColor: hasTrueColorHint, Hyperlinks: false})
}

func probeTmuxHyperlinks() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_termfeatures}").Output()
	if err != nil {
		return false
	}
	for _, feature := range strings.Split(string(out), ",") {
		if strings.TrimSpace(feature) == "hyperlinks" {
			return true
		}
	}
	return false
}

func normalizeCapabilities(c TerminalCapabilities) TerminalCapabilities {
	switch c.Images {
	case ImageProtocolKitty:
		c.Kitty = true
		c.ITerm2 = false
	case ImageProtocolITerm2:
		c.ITerm2 = true
		c.Kitty = false
	default:
		if c.Kitty {
			c.Images = ImageProtocolKitty
		} else if c.ITerm2 {
			c.Images = ImageProtocolITerm2
		}
	}
	return c
}

func AllocateImageID() int {
	return int(nextImageID.Add(1) - 1)
}

// CalculateImageCellSize returns the size in terminal cells that an image
// would occupy when laid out within the given max width / max height (in
// cells), preserving aspect ratio.
//
// Mirrors upstream calculateImageCellSize(): scale = min(widthScale,
// heightScale) where widthScale = maxW*cellW/imgW and heightScale =
// maxH*cellH/imgH. Final columns/rows are ceil(scaled / cell), clamped.
func CalculateImageCellSize(dim ImageDimensions, maxWidthCells int, maxHeightCells ...int) ImageCellSize {
	maxWidth := maxInt(1, maxWidthCells)
	maxHeight := 0
	hasMaxHeight := false
	if len(maxHeightCells) > 0 {
		hasMaxHeight = true
		maxHeight = maxInt(1, maxHeightCells[0])
	}
	cd := cellDimsSnapshot()
	if dim.Width <= 0 || dim.Height <= 0 || cd.Width <= 0 || cd.Height <= 0 {
		rows := 1
		if hasMaxHeight {
			rows = minInt(rows, maxHeight)
		}
		return ImageCellSize{Columns: minInt(1, maxWidth), Rows: rows}
	}

	imageW := maxInt(1, dim.Width)
	imageH := maxInt(1, dim.Height)

	wsNum := maxWidth * cd.Width
	wsDen := imageW
	hsNum := wsNum
	hsDen := wsDen
	if hasMaxHeight {
		hsNum = maxHeight * cd.Height
		hsDen = imageH
	}
	useHeightScale := hasMaxHeight && hsNum*wsDen < wsNum*hsDen
	scaleNum, scaleDen := wsNum, wsDen
	if useHeightScale {
		scaleNum, scaleDen = hsNum, hsDen
	}
	scaledWidthPx := imageW * scaleNum / scaleDen
	scaledHeightPx := imageH * scaleNum / scaleDen

	columns := ceilDiv(maxInt(1, scaledWidthPx), cd.Width)
	rows := ceilDiv(maxInt(1, scaledHeightPx), cd.Height)
	columns = maxInt(1, minInt(maxWidth, columns))
	if hasMaxHeight {
		rows = maxInt(1, minInt(maxHeight, rows))
	} else {
		rows = maxInt(1, rows)
	}
	return ImageCellSize{Columns: columns, Rows: rows}
}

func CalculateImageRows(dim ImageDimensions, maxWidthCells int) int {
	return CalculateImageCellSize(dim, maxWidthCells).Rows
}

func GetImageDimensions(data []byte) (ImageDimensions, error) {
	if dim, err := GetPngDimensions(data); err == nil {
		return dim, nil
	}
	if dim, err := GetJpegDimensions(data); err == nil {
		return dim, nil
	}
	if dim, err := GetGifDimensions(data); err == nil {
		return dim, nil
	}
	if dim, err := GetWebpDimensions(data); err == nil {
		return dim, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ImageDimensions{}, err
	}
	return ImageDimensions{Width: cfg.Width, Height: cfg.Height}, nil
}

func GetPngDimensions(data []byte) (ImageDimensions, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ImageDimensions{}, err
	}
	return ImageDimensions{Width: cfg.Width, Height: cfg.Height}, nil
}

func GetJpegDimensions(data []byte) (ImageDimensions, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ImageDimensions{}, err
	}
	return ImageDimensions{Width: cfg.Width, Height: cfg.Height}, nil
}

func GetGifDimensions(data []byte) (ImageDimensions, error) {
	cfg, err := gif.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ImageDimensions{}, err
	}
	return ImageDimensions{Width: cfg.Width, Height: cfg.Height}, nil
}

func GetWebpDimensions(data []byte) (ImageDimensions, error) {
	if len(data) < 30 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return ImageDimensions{}, fmt.Errorf("not webp")
	}
	chunk := string(data[12:16])
	switch chunk {
	case "VP8X":
		if len(data) < 30 {
			return ImageDimensions{}, fmt.Errorf("short webp")
		}
		w := int(data[24]) | int(data[25])<<8 | int(data[26])<<16
		h := int(data[27]) | int(data[28])<<8 | int(data[29])<<16
		return ImageDimensions{Width: w + 1, Height: h + 1}, nil
	case "VP8 ":
		if len(data) < 30 {
			return ImageDimensions{}, fmt.Errorf("short webp")
		}
		w := int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
		return ImageDimensions{Width: w, Height: h}, nil
	case "VP8L":
		if len(data) < 25 {
			return ImageDimensions{}, fmt.Errorf("short webp")
		}
		b0, b1, b2, b3 := uint32(data[21]), uint32(data[22]), uint32(data[23]), uint32(data[24])
		w := int(1 + (((b1 & 0x3f) << 8) | b0))
		h := int(1 + ((b3 << 6) | (b2 << 14) | ((b1 & 0xc0) >> 6)))
		return ImageDimensions{Width: w, Height: h}, nil
	default:
		return ImageDimensions{}, fmt.Errorf("unsupported webp chunk")
	}
}

type ImageRenderOptions struct {
	ID         int
	Width      int
	Height     int
	MaxWidth   int
	MaxHeight  int
	MoveCursor *bool
}

func EncodeKitty(data []byte, options ImageRenderOptions) string {
	id := options.ID
	if id == 0 {
		id = AllocateImageID()
	}
	params := []string{"a=T", "f=100", "q=2"}
	if options.MoveCursor != nil && !*options.MoveCursor {
		params = append(params, "C=1")
	}
	if options.Width > 0 {
		params = append(params, fmt.Sprintf("c=%d", options.Width))
	}
	if options.Height > 0 {
		params = append(params, fmt.Sprintf("r=%d", options.Height))
	}
	if id > 0 {
		params = append(params, fmt.Sprintf("i=%d", id))
	}
	base64Data := base64.StdEncoding.EncodeToString(data)
	const chunkSize = 4096
	if len(base64Data) <= chunkSize {
		return fmt.Sprintf("\x1b_G%s;%s\x1b\\", strings.Join(params, ","), base64Data)
	}
	var b strings.Builder
	for offset, first := 0, true; offset < len(base64Data); first = false {
		end := offset + chunkSize
		if end > len(base64Data) {
			end = len(base64Data)
		}
		chunk := base64Data[offset:end]
		last := end == len(base64Data)
		switch {
		case first:
			fmt.Fprintf(&b, "\x1b_G%s,m=1;%s\x1b\\", strings.Join(params, ","), chunk)
		case last:
			fmt.Fprintf(&b, "\x1b_Gm=0;%s\x1b\\", chunk)
		default:
			fmt.Fprintf(&b, "\x1b_Gm=1;%s\x1b\\", chunk)
		}
		offset = end
	}
	return b.String()
}

func EncodeITerm2(data []byte, name string) string {
	if name == "" {
		name = "image"
	}
	return fmt.Sprintf("\x1b]1337;File=name=%s;inline=1:%s\a", base64.StdEncoding.EncodeToString([]byte(name)), base64.StdEncoding.EncodeToString(data))
}

func RenderImage(data []byte, options ImageRenderOptions) string {
	caps := GetCapabilities()
	if caps.Kitty || caps.Images == ImageProtocolKitty {
		return EncodeKitty(data, options)
	}
	if caps.ITerm2 || caps.Images == ImageProtocolITerm2 {
		return EncodeITerm2(data, "image")
	}
	return ImageFallback(data)
}

func ImageFallback(data []byte) string {
	dim, err := GetImageDimensions(data)
	if err != nil {
		return "[Image]"
	}
	return fmt.Sprintf("[Image %dx%d]", dim.Width, dim.Height)
}

func DeleteKittyImage(id int) string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
}

func DeleteAllKittyImages() string {
	return "\x1b_Ga=d,d=A,q=2\x1b\\"
}

func Hyperlink(text, url string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func IsImageLine(line string) bool {
	return strings.Contains(line, "\x1b_G") || strings.Contains(line, "\x1b]1337;File=")
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
