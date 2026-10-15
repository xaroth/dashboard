package render_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/xaroth/dashboard/render"
)

func TestGray(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		in     color.Color
		levels int
		want   uint8
	}{
		{name: "black", in: color.Black, levels: 4, want: 0},
		{name: "white", in: color.White, levels: 4, want: 255},
		{name: "mid gray rounds to nearest of 4", in: color.Gray{Y: 100}, levels: 4, want: 85},
		{name: "mid gray rounds to nearest of 16", in: color.Gray{Y: 100}, levels: 16, want: 102},
		{name: "transparent becomes paper", in: color.Transparent, levels: 4, want: 255},
		{name: "quarter opaque black composites onto white", in: color.NRGBA{A: 64}, levels: 4, want: 170},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
			src.Set(0, 0, testCase.in)

			got := render.Gray(src, testCase.levels).At(0, 0).(color.Gray).Y
			if got != testCase.want {
				t.Errorf("got %d, want %d", got, testCase.want)
			}
		})
	}
}

func TestEncodeBitDepth(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		levels int
		want   byte
	}{
		{name: "4 levels", levels: 4, want: 2},
		{name: "16 levels", levels: 16, want: 4},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			img := render.Gray(image.NewGray(image.Rect(0, 0, 8, 8)), testCase.levels)
			data, err := render.Encode(img)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if _, err := png.Decode(bytes.NewReader(data)); err != nil {
				t.Fatalf("decoding output: %v", err)
			}
			// IHDR bit depth sits right after the 8-byte signature, chunk header and width/height.
			if got := data[24]; got != testCase.want {
				t.Errorf("got bit depth %d, want %d", got, testCase.want)
			}
		})
	}
}
