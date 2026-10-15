package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/service/render"
	"github.com/xaroth/dashboard/service/web"
)

// stubPipeline records the target it was asked for. Page "nope" does not
// exist, model "kindle" is unknown and page "broken" fails to render.
type stubPipeline struct {
	mu     sync.Mutex
	target render.Target
}

func (s *stubPipeline) Pages(context.Context) ([]string, error) { return []string{"hello"}, nil }

func (s *stubPipeline) Props(_ context.Context, t render.Target, page string) (render.Props, error) {
	s.mu.Lock()
	s.target = t
	s.mu.Unlock()
	switch {
	case page == "nope":
		return render.Props{}, fmt.Errorf("%w %q", render.ErrUnknownPage, page)
	case t.Model == "kindle":
		return render.Props{}, fmt.Errorf("%w %q", render.ErrUnknownModel, t.Model)
	case page == "broken":
		return render.Props{}, errors.New("renderer: boom")
	}
	return render.Props{Device: render.DeviceProps{Name: t.Name, Model: t.Model, Rotation: t.Rotation}, Now: "now"}, nil
}

func (s *stubPipeline) Render(ctx context.Context, t render.Target, page string) (render.Image, error) {
	props, err := s.Props(ctx, t, page)
	if err != nil {
		return render.Image{}, err
	}
	return render.Image{PNG: []byte("png"), Filename: "f", Props: props}, nil
}

func newHandler(tb testing.TB, devices ...web.Device) (http.Handler, *stubPipeline) {
	tb.Helper()
	if devices == nil {
		devices = []web.Device{{Name: "hallway", Pages: []string{"hello"}, Rotation: 90}}
	}
	stub := &stubPipeline{}
	h := &web.Handler{
		Pipeline: stub,
		Devices:  devices,
		Target: func(name string) (render.Target, bool) {
			if name != "hallway" {
				return render.Target{}, false
			}
			return render.Target{Name: "hallway", Model: "reterminal_e1001", Rotation: 90}, true
		},
		Log: slog.New(slog.DiscardHandler),
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, stub
}

func get(tb testing.TB, h http.Handler, url string) *httptest.ResponseRecorder {
	tb.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

func TestIndex(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	rec := get(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	for _, want := range []string{
		`src="/preview/hallway/hello.png"`,
		`href="/preview/hallway/hello/props.json"`,
		`src="/preview/page/hello.png?model=reterminal_e1003&rotation=90"`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("index lacks %s", want)
		}
	}
}

func TestIndexEscapesNames(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t, web.Device{Name: `<script>x</script>`, Pages: []string{"hello"}})
	body := get(t, h, "/").Body.String()
	if strings.Contains(body, "<script>x") {
		t.Error("device name rendered unescaped")
	}
}

func TestIndexWithoutDevices(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t, []web.Device{}...)
	rec := get(t, h, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No devices configured") {
		t.Errorf("got %d, want 200 with \"No devices configured\"", rec.Code)
	}
}

func TestPreviewPNG(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		url        string
		wantStatus int
		wantTarget render.Target
	}{
		{"device", "/preview/hallway/hello.png", http.StatusOK, render.Target{Name: "hallway", Model: "reterminal_e1001", Rotation: 90}},
		{"device with model", "/preview/hallway/hello.png?model=reterminal_e1003", http.StatusOK, render.Target{Name: "hallway", Model: "reterminal_e1003", Rotation: 90}},
		{"panel defaults", "/preview/page/hello.png", http.StatusOK, render.Target{Name: "preview", Model: "reterminal_e1001"}},
		{"panel", "/preview/page/hello.png?model=reterminal_e1003&rotation=270", http.StatusOK, render.Target{Name: "preview", Model: "reterminal_e1003", Rotation: 270}},
		{"unknown device", "/preview/garage/hello.png", http.StatusNotFound, render.Target{}},
		{"not png", "/preview/hallway/hello.jpg", http.StatusNotFound, render.Target{}},
		{"bad rotation", "/preview/page/hello.png?rotation=45", http.StatusBadRequest, render.Target{}},
		{"unknown page", "/preview/page/nope.png", http.StatusNotFound, render.Target{Name: "preview", Model: "reterminal_e1001"}},
		{"unknown model", "/preview/hallway/hello.png?model=kindle", http.StatusBadRequest, render.Target{Name: "hallway", Model: "kindle", Rotation: 90}},
		{"render error", "/preview/page/broken.png", http.StatusInternalServerError, render.Target{Name: "preview", Model: "reterminal_e1001"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			h, stub := newHandler(t)
			rec := get(t, h, testCase.url)
			if rec.Code != testCase.wantStatus {
				t.Fatalf("got %d %s, want %d", rec.Code, rec.Body, testCase.wantStatus)
			}
			if diff := cmp.Diff(testCase.wantTarget, stub.target); diff != "" {
				t.Errorf("target (-want +got):\n%s", diff)
			}
			if rec.Code == http.StatusOK && rec.Header().Get("Content-Type") != "image/png" {
				t.Errorf("got content type %q, want image/png", rec.Header().Get("Content-Type"))
			}
		})
	}
}

func TestPreviewProps(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		url        string
		wantStatus int
		want       *render.Props
	}{
		{"device", "/preview/hallway/hello/props.json", http.StatusOK, &render.Props{
			Device: render.DeviceProps{Name: "hallway", Model: "reterminal_e1001", Rotation: 90},
			Now:    "now",
		}},
		{"unknown device", "/preview/garage/hello/props.json", http.StatusNotFound, nil},
		{"unknown page", "/preview/hallway/nope/props.json", http.StatusNotFound, nil},
		{"render error", "/preview/hallway/broken/props.json", http.StatusInternalServerError, nil},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			h, _ := newHandler(t)
			rec := get(t, h, testCase.url)
			if rec.Code != testCase.wantStatus {
				t.Fatalf("got %d %s, want %d", rec.Code, rec.Body, testCase.wantStatus)
			}
			if testCase.want == nil {
				return
			}
			var got render.Props
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding props: %v", err)
			}
			if diff := cmp.Diff(*testCase.want, got); diff != "" {
				t.Errorf("props (-want +got):\n%s", diff)
			}
		})
	}
}
