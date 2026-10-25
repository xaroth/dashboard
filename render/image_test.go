package render_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/render"
)

func encodePNG(tb testing.TB, img image.Image) []byte {
	tb.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		tb.Fatalf("encoding png: %v", err)
	}
	return buf.Bytes()
}

func grays(tb testing.TB, pngData []byte) [][]uint8 {
	tb.Helper()
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		tb.Fatalf("decoding png: %v", err)
	}
	b := img.Bounds()
	out := make([][]uint8, b.Dy())
	for y := range out {
		out[y] = make([]uint8, b.Dx())
		for x := range out[y] {
			out[y][x] = color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y
		}
	}
	return out
}

func TestFinish(t *testing.T) {
	t.Parallel()
	// Top row: black, dark, light, white. Bottom row: transparent, then black at quarter alpha (~191 on paper).
	levels := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for x, v := range []uint8{0, 80, 180, 255} {
		levels.Set(x, 0, color.NRGBA{v, v, v, 255})
	}
	levels.Set(1, 1, color.NRGBA{0, 0, 0, 64})
	levels.Set(2, 1, color.NRGBA{0, 0, 0, 64})
	levels.Set(3, 1, color.NRGBA{0, 0, 0, 64})
	// png.Decode returns *image.Gray here, which takes the generic pixel path.
	gray := image.NewGray(image.Rect(0, 0, 2, 1))
	gray.Pix = []uint8{40, 200}

	testCases := []struct {
		name     string
		src      image.Image
		rotation int
		want     [][]uint8
	}{
		{"levels", levels, 0, [][]uint8{{0, 85, 170, 255}, {255, 170, 170, 170}}},
		{"turned", levels, 180, [][]uint8{{170, 170, 170, 255}, {255, 170, 85, 0}}},
		{"gray input", gray, 0, [][]uint8{{0, 170}}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			out, err := render.Finish(encodePNG(t, testCase.src), testCase.rotation)
			if err != nil {
				t.Fatalf("got error %v, want nil", err)
			}
			// IHDR bit depth and color type: 2-bit grayscale.
			if out[24] != 2 || out[25] != 0 {
				t.Errorf("got bit depth %d, color type %d, want 2, 0", out[24], out[25])
			}
			if diff := cmp.Diff(testCase.want, grays(t, out)); diff != "" {
				t.Errorf("pixels (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFinishErrors(t *testing.T) {
	t.Parallel()
	// Uniform noise over the 4 levels does not compress: 2400x1600 at 2 bits is 960 KB.
	noise := image.NewGray(image.Rect(0, 0, 2400, 1600))
	seed := uint32(1)
	for i := range noise.Pix {
		seed = seed*1664525 + 1013904223
		noise.Pix[i] = uint8(seed>>30) * 85
	}
	testCases := []struct {
		name string
		in   []byte
	}{
		{"over the firmware limit", encodePNG(t, noise)},
		{"not a png", []byte("nope")},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := render.Finish(testCase.in, 0); err == nil {
				t.Error("got nil error, want one")
			}
		})
	}
}

func TestPanelFor(t *testing.T) {
	t.Parallel()
	for _, model := range render.Models() {
		if p, ok := render.PanelFor(model); !ok || p.Width == 0 {
			t.Errorf("PanelFor(%q) = %v, %v; want a panel", model, p, ok)
		}
	}
	if want := []string{"reterminal_e1001", "reterminal_e1003"}; !slices.Equal(render.Models(), want) {
		t.Errorf("got models %v, want %v", render.Models(), want)
	}
	if _, ok := render.PanelFor("kindle"); ok {
		t.Error("got a panel for kindle, want none")
	}
}

func TestViewportFor(t *testing.T) {
	t.Parallel()
	panel, _ := render.PanelFor("reterminal_e1003")
	testCases := []struct {
		name     string
		rotation int
		want     render.Viewport
	}{
		{"landscape", 0, render.Viewport{Width: 1872, Height: 1404}},
		{"portrait", 90, render.Viewport{Width: 1404, Height: 1872, Portrait: true}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(testCase.want, render.ViewportFor(panel, testCase.rotation)); diff != "" {
				t.Errorf("viewport (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFilename(t *testing.T) {
	t.Parallel()
	got := render.Filename([]byte("png"), time.Unix(1792087200, 0))
	if len(got) != 23 || got[12:] != "-1792087200" {
		t.Errorf("got %q, want 12 hex + \"-1792087200\"", got)
	}
	if other := render.Filename([]byte("other"), time.Unix(1792087200, 0)); other[:12] == got[:12] {
		t.Errorf("got hash %q for different images, want a different one", other[:12])
	}
}

func BenchmarkFinishE1003(b *testing.B) {
	panel, _ := render.PanelFor("reterminal_e1003")
	img := image.NewNRGBA(image.Rect(0, 0, panel.Width, panel.Height))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	in := encodePNG(b, img)
	for b.Loop() {
		if _, err := render.Finish(in, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// pngSize reads width and height from the IHDR chunk.
func pngSize(data []byte) (int, int) {
	return int(binary.BigEndian.Uint32(data[16:])), int(binary.BigEndian.Uint32(data[20:]))
}
