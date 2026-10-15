// Package render turns HTML into e-ink ready PNGs by running the Takumi
// renderer binary and reducing its output to the panel's gray levels.
package render

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os/exec"
	"strconv"
)

// Panel describes the image a device expects.
type Panel struct {
	Width  int
	Height int
	// Levels is the number of gray levels the panel shows: 2, 4 or 16.
	Levels int
}

var (
	E1001 = Panel{Width: 800, Height: 480, Levels: 4}
	E1003 = Panel{Width: 1872, Height: 1404, Levels: 16}
)

// Renderer runs the renderer binary at Bin with the given fonts and stylesheet.
type Renderer struct {
	Bin   string
	CSS   string
	Fonts []string
}

// Render renders html at the panel size and returns it reduced to the panel's gray levels.
func (r *Renderer) Render(ctx context.Context, html string, p Panel) (*image.Paletted, error) {
	args := []string{"--width", strconv.Itoa(p.Width), "--height", strconv.Itoa(p.Height)}
	if r.CSS != "" {
		args = append(args, "--css", r.CSS)
	}
	for _, f := range r.Fonts {
		args = append(args, "--font", f)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Stdin = bytes.NewBufferString(html)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("running renderer: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}

	img, err := png.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("decoding renderer output: %w", err)
	}
	return Gray(img, p.Levels), nil
}

// Gray maps every pixel to the nearest of n evenly spaced gray levels.
// No dithering: text edges stay sharp, which matters more on e-ink than smooth gradients.
func Gray(img image.Image, n int) *image.Paletted {
	pal := make(color.Palette, n)
	for i := range n {
		v := uint8(i * 255 / (n - 1))
		pal[i] = color.Gray{Y: v}
	}

	b := img.Bounds()
	out := image.NewPaletted(b, pal)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			// Composite onto white so transparent areas become paper, not ink.
			r, g, bl, a := img.At(x, y).RGBA()
			white := 0xffff - a
			lum := (299*(r+white) + 587*(g+white) + 114*(bl+white)) / 1000
			level := (int(lum)*(n-1) + 0x7fff) / 0xffff
			out.SetColorIndex(x, y, uint8(level))
		}
	}
	return out
}

// Encode writes img as a PNG. Go picks the smallest bit depth for the palette,
// so 4 levels encode as 2-bit and 16 levels as 4-bit.
func Encode(img *image.Paletted) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encoding png: %w", err)
	}
	return buf.Bytes(), nil
}
