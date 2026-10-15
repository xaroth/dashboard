package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"
)

func encode(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFinishLevelsAndRotation(t *testing.T) {
	// Left to right: black, dark, light, white; bottom row transparent.
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for x, v := range []uint8{0, 80, 180, 255} {
		src.Set(x, 0, color.NRGBA{v, v, v, 255})
		src.Set(x, 1, color.NRGBA{0, 0, 0, 0})
	}
	for _, tt := range []struct {
		rotation int
		want     [][]uint8
	}{
		{0, [][]uint8{{0, 85, 170, 255}, {255, 255, 255, 255}}},
		{90, [][]uint8{{0, 85, 170, 255}, {255, 255, 255, 255}}},
		{180, [][]uint8{{255, 255, 255, 255}, {255, 170, 85, 0}}},
		{270, [][]uint8{{255, 255, 255, 255}, {255, 170, 85, 0}}},
	} {
		out, err := Finish(encode(t, src), tt.rotation)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		for y, row := range tt.want {
			for x, v := range row {
				if got := color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y; got != v {
					t.Errorf("rotation %d: (%d,%d) = %d, want %d", tt.rotation, x, y, got, v)
				}
			}
		}
	}
}

func TestFinishIs2Bit(t *testing.T) {
	out, err := Finish(encode(t, image.NewRGBA(image.Rect(0, 0, 8, 8))), 0)
	if err != nil {
		t.Fatal(err)
	}
	// IHDR bit depth is at byte 24.
	if out[24] != 2 {
		t.Errorf("bit depth = %d, want 2", out[24])
	}
}

func TestFinishTooLarge(t *testing.T) {
	// Uniform noise over the 4 levels does not compress: 2400x1600 at 2 bits is 960 KB.
	src := image.NewGray(image.Rect(0, 0, 2400, 1600))
	seed := uint32(1)
	for i := range src.Pix {
		seed = seed*1664525 + 1013904223
		src.Pix[i] = uint8(seed>>30) * 85
	}
	if _, err := Finish(encode(t, src), 0); err == nil {
		t.Error("Finish accepted an image over the size limit")
	}
}

func TestViewportFor(t *testing.T) {
	p := Panels["reterminal_e1003"]
	if got := ViewportFor(p, 0); got != (Viewport{1872, 1404, false}) {
		t.Errorf("0: %+v", got)
	}
	if got := ViewportFor(p, 270); got != (Viewport{1404, 1872, true}) {
		t.Errorf("270: %+v", got)
	}
}

func TestFilename(t *testing.T) {
	name := Filename([]byte("png"), time.Unix(1792087200, 0))
	if len(name) > 31 || name[12:] != "-1792087200" {
		t.Errorf("Filename = %q", name)
	}
}
