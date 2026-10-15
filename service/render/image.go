package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"time"
)

// MaxImageSize is the firmware's PNG limit with PSRAM, for both models.
const MaxImageSize = 750 << 10

// Panel is a device's native screen size, landscape.
type Panel struct {
	Width, Height int
}

// Panels by the Model header the firmware sends.
var Panels = map[string]Panel{
	"reterminal_e1001": {800, 480},
	"reterminal_e1003": {1872, 1404},
}

// ViewportFor returns the size pages render at: 90 and 270 swap the panel's
// sides, and the firmware rotates a portrait image itself.
func ViewportFor(p Panel, rotation int) Viewport {
	if rotation == 90 || rotation == 270 {
		return Viewport{Width: p.Height, Height: p.Width, Portrait: true}
	}
	return Viewport{Width: p.Width, Height: p.Height}
}

// Both models get the E1001's four levels so one set of styles works on both.
var grays = color.Palette{color.Gray{0}, color.Gray{85}, color.Gray{170}, color.Gray{255}}

// Finish turns a rendered PNG into what the device gets: turned 180° for
// rotation 180 and 270, reduced to 4 grays, encoded as a 2-bit PNG.
func Finish(rendered []byte, rotation int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(rendered))
	if err != nil {
		return nil, fmt.Errorf("decoding rendered png: %w", err)
	}
	out := reduce(img, rotation == 180 || rotation == 270)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, out); err != nil {
		return nil, fmt.Errorf("encoding png: %w", err)
	}
	if buf.Len() > MaxImageSize {
		return nil, fmt.Errorf("png is %d bytes, over the %d byte firmware limit", buf.Len(), MaxImageSize)
	}
	return buf.Bytes(), nil
}

// reduce maps every pixel to the nearest gray without dithering, which keeps
// text edges sharp. Transparent areas become paper.
func reduce(img image.Image, turn bool) *image.Paletted {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewPaletted(image.Rect(0, 0, w, h), grays)
	nrgba, isNRGBA := img.(*image.NRGBA)
	for y := range h {
		for x := range w {
			var r, g, bl, a uint32
			if isNRGBA {
				i := nrgba.PixOffset(b.Min.X+x, b.Min.Y+y)
				p := nrgba.Pix[i : i+4 : i+4]
				a = uint32(p[3]) * 0x101
				r, g, bl = uint32(p[0])*0x101*a/0xffff, uint32(p[1])*0x101*a/0xffff, uint32(p[2])*0x101*a/0xffff
			} else {
				r, g, bl, a = img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			}
			white := 0xffff - a
			lum := (299*(r+white) + 587*(g+white) + 114*(bl+white)) / 1000
			level := uint8((lum*3 + 0x7fff) / 0xffff)
			ox, oy := x, y
			if turn {
				ox, oy = w-1-x, h-1-y
			}
			out.Pix[oy*out.Stride+ox] = level
		}
	}
	return out
}

// Filename is the device's cache key: 12 hex of the PNG's hash plus a Unix
// timestamp, 23 characters. The firmware truncates names over 31 characters
// and purges cached names without a 10-digit timestamp suffix.
func Filename(png []byte, at time.Time) string {
	sum := sha256.Sum256(png)
	return hex.EncodeToString(sum[:6]) + "-" + strconv.FormatInt(at.Unix(), 10)
}
