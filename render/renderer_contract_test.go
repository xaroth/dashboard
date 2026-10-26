package render_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/xaroth/dashboard/render"
)

// TestRendererContract holds FakeRenderer to what Process does, with the
// test-binary renderer and with Bun.
func TestRendererContract(t *testing.T) {
	t.Parallel()
	type renderer interface {
		render.Renderer
		Close() error
	}
	testCases := []struct {
		name string
		page string // a page the renderer has
		new  func(testing.TB) renderer
	}{
		{"fake", "hello", func(tb testing.TB) renderer {
			return render.NewFakeRenderer(tb, render.ExpectPages(render.PageInfo{Name: "hello"}))
		}},
		{"process", "hello", func(tb testing.TB) renderer {
			p, _ := newFake(tb, "ok", nil)
			return p
		}},
		{"bun", "hello", func(tb testing.TB) renderer { return newBun(tb) }},
	}
	props := render.Props{
		Data:     map[string]render.Result{},
		Device:   render.DeviceProps{Name: "t", Model: "reterminal_e1001"},
		Viewport: render.Viewport{Width: 800, Height: 480},
		System:   render.SystemProps{StaleModules: []string{}},
		Now:      "2026-10-16T05:30:00Z",
		Timezone: "UTC",
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			r := testCase.new(t)

			pages, err := r.Describe(t.Context())
			if err != nil {
				t.Fatalf("Describe: %v", err)
			}
			var names []string
			for _, p := range pages {
				names = append(names, p.Name)
			}
			if !slices.Contains(names, testCase.page) {
				t.Errorf("got pages %v, want %s among them", names, testCase.page)
			}

			if png, err := r.Render(t.Context(), testCase.page, props); err != nil || len(png) == 0 {
				t.Errorf("got Render %d bytes, %v, want a png", len(png), err)
			}
			if _, err := r.Render(t.Context(), "nope", props); err == nil || err.Error() != "renderer: unknown page nope" {
				t.Errorf("got %v, want renderer: unknown page nope", err)
			}

			r.Close()
			if _, err := r.Render(t.Context(), testCase.page, props); !errors.Is(err, render.ErrClosed) {
				t.Errorf("got Render %v after Close, want ErrClosed", err)
			}
			if _, err := r.Describe(t.Context()); !errors.Is(err, render.ErrClosed) {
				t.Errorf("got Describe %v after Close, want ErrClosed", err)
			}
		})
	}
}
