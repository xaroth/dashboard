package render

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"maps"
	"slices"
	"strconv"
	"time"
)

// MaxImageSize is the firmware's PNG limit with PSRAM, for both models.
const MaxImageSize = 750 << 10

// Panel is a device's native screen size, landscape.
type Panel struct {
	Width, Height int
}

// panels by the Model header the firmware sends.
var panels = map[string]Panel{
	"reterminal_e1001": {800, 480},
	"reterminal_e1003": {1872, 1404},
}

// PanelFor returns the panel of model, as the firmware's Model header names it.
func PanelFor(model string) (Panel, bool) {
	p, ok := panels[model]
	return p, ok
}

// Models returns the known models, sorted.
func Models() []string {
	return slices.Sorted(maps.Keys(panels))
}

// ViewportFor returns the size pages render at: 90 and 270 swap the panel's
// sides, and the firmware rotates a portrait image itself.
func ViewportFor(p Panel, rotation int) Viewport {
	if rotation == 90 || rotation == 270 {
		return Viewport{Width: p.Height, Height: p.Width, Portrait: true}
	}
	return Viewport{Width: p.Width, Height: p.Height}
}

// Finish turns a rendered PNG into what the device gets: turned 180° for
// rotation 180 and 270, reduced to 4 grays, encoded as a 2-bit grayscale PNG.
// Both models get the E1001's four levels so one set of styles works on both.
// An image over MaxImageSize is an error; the caller keeps serving the previous one.
func Finish(rendered []byte, rotation int) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(rendered))
	if err != nil {
		return nil, fmt.Errorf("decoding rendered png: %w", err)
	}
	b := img.Bounds()
	out, err := encodeGray2(reduce(img, rotation == 180 || rotation == 270), b.Dx(), b.Dy())
	if err != nil {
		return nil, err
	}
	if len(out) > MaxImageSize {
		return nil, fmt.Errorf("png is %d bytes, over the %d byte firmware limit", len(out), MaxImageSize)
	}
	return out, nil
}

// reduce maps every pixel to the nearest of 4 gray levels (0-3) without
// dithering, which keeps text edges sharp. Transparent areas become paper.
func reduce(img image.Image, turn bool) []uint8 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]uint8, w*h)
	nrgba, isNRGBA := img.(*image.NRGBA)
	for y := range h {
		for x := range w {
			var r, g, bl, a uint32
			if isNRGBA {
				i := nrgba.PixOffset(b.Min.X+x, b.Min.Y+y)
				p := nrgba.Pix[i : i+4 : i+4]
				// Premultiply like color.RGBA(); 0xffff*0xffff still fits in uint32.
				a = uint32(p[3]) * 0x101
				r, g, bl = uint32(p[0])*0x101*a/0xffff, uint32(p[1])*0x101*a/0xffff, uint32(p[2])*0x101*a/0xffff
			} else {
				r, g, bl, a = img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			}
			white := 0xffff - a
			lum := (299*(r+white) + 587*(g+white) + 114*(bl+white)) / 1000
			ox, oy := x, y
			if turn {
				ox, oy = w-1-x, h-1-y
			}
			out[oy*w+ox] = uint8((lum*3 + 0x7fff) / 0xffff)
		}
	}
	return out
}

// encodeGray2 writes levels (0-3, row-major) as a 2-bit grayscale PNG, the
// format TRMNL serves to 4-gray panels. image/png has no 2-bit gray encoder.
func encodeGray2(levels []uint8, w, h int) ([]byte, error) {
	stride := (w + 3) / 4
	raw := make([]byte, h*(1+stride))
	for y := range h {
		row := raw[y*(1+stride)+1 : (y+1)*(1+stride)] // leading byte: filter type 0
		for x := range w {
			row[x/4] |= levels[y*w+x] << (6 - 2*(x%4))
		}
	}
	var idat bytes.Buffer
	zw, err := zlib.NewWriterLevel(&idat, zlib.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("compressing image: %w", err)
	}
	zw.Write(raw)
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compressing image: %w", err)
	}

	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9] = 2, 0 // bit depth 2, color type gray
	writeChunk(&out, "IHDR", ihdr)
	writeChunk(&out, "IDAT", idat.Bytes())
	writeChunk(&out, "IEND", nil)
	return out.Bytes(), nil
}

func writeChunk(w *bytes.Buffer, typ string, data []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data)))
	w.Write(n[:])
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	w.WriteString(typ)
	w.Write(data)
	binary.BigEndian.PutUint32(n[:], crc.Sum32())
	w.Write(n[:])
}

// Filename is the device's cache key: 12 hex of the PNG's hash plus a Unix
// timestamp, 23 characters. The firmware truncates names over 31 characters
// and purges cached names without a 10-digit timestamp suffix.
func Filename(png []byte, at time.Time) string {
	sum := sha256.Sum256(png)
	return hex.EncodeToString(sum[:6]) + "-" + strconv.FormatInt(at.Unix(), 10)
}
