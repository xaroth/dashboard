// package main cannot be imported, so its tests are internal.
package main

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
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/device"
	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/render"
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

type stubModules struct{}

func (stubModules) Update(_ context.Context, name string) error {
	switch name {
	case "weather":
		return nil
	case "car":
		return errors.New("token refused")
	}
	return fmt.Errorf("%w %q", module.ErrUnknownModule, name)
}

func (stubModules) Statuses(context.Context) ([]module.Status, error) {
	fetched := time.Date(2026, 10, 16, 7, 0, 0, 0, time.UTC)
	return []module.Status{
		{Name: "weather", Phase: module.PhaseWaiting, Interval: 15 * time.Minute, FetchedAt: &fetched},
		{Name: "car", Phase: module.PhaseAuth, Error: "token <refused>", Stale: true},
	}, nil
}

func newHandler(tb testing.TB, devices ...device.Config) (http.Handler, *stubPipeline) {
	tb.Helper()
	if devices == nil {
		devices = []device.Config{{Name: "hallway", MAC: "AA:BB:CC:DD:EE:FF", Pages: []string{"hello"}, Rotation: 90}}
	}
	stub := &stubPipeline{}
	devs := device.New(device.Options{
		Devices:   devices,
		Pipeline:  stub,
		Store:     &device.Store{Max: 4},
		PublicURL: "http://dash",
		Schedule:  device.Schedule{First: 7 * time.Hour, Interval: time.Hour, Detail: 5 * time.Minute, Location: time.UTC},
		Log:       slog.New(slog.DiscardHandler),
		Now:       func() time.Time { return time.Unix(1792087200, 0) },
	})
	deviceAPI := http.NewServeMux()
	devs.Register(deviceAPI)
	h := &adminHandler{
		Pipeline:  stub,
		Devices:   devs,
		Modules:   stubModules{},
		DeviceAPI: deviceAPI,
		Log:       slog.New(slog.DiscardHandler),
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
		`<td>weather</td>`,
		`<td>15m0s</td>`,
		`auth <span class="stale">stale</span>`,
		`token &lt;refused&gt;`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("index lacks %s", want)
		}
	}
}

func TestIndexEscapesNames(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t, device.Config{Name: `<script>x</script>`, MAC: "AA:BB:CC:DD:EE:FF", Pages: []string{"hello"}})
	body := get(t, h, "/").Body.String()
	if strings.Contains(body, "<script>x") {
		t.Error("device name rendered unescaped")
	}
}

func TestIndexWithoutDevices(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t, []device.Config{}...)
	rec := get(t, h, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No devices configured") {
		t.Errorf("got %d, want 200 with \"No devices configured\"", rec.Code)
	}
}

func TestUpdateModule(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		module string
		want   int
	}{
		{"weather", http.StatusSeeOther},
		{"car", http.StatusSeeOther}, // the failure shows in the table
		{"nope", http.StatusNotFound},
	}
	for _, testCase := range testCases {
		t.Run(testCase.module, func(t *testing.T) {
			t.Parallel()
			h, _ := newHandler(t)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/modules/"+testCase.module+"/update", nil))
			if rec.Code != testCase.want {
				t.Errorf("got %d, want %d", rec.Code, testCase.want)
			}
		})
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

func TestSimulateWake(t *testing.T) {
	t.Parallel()
	h, stub := newHandler(t)

	post := func(url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, url, nil))
		return rec
	}
	type exchange struct {
		Status   int            `json:"status"`
		Response map[string]any `json:"response"`
	}
	decode := func(rec *httptest.ResponseRecorder) []exchange {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d %s, want 200", rec.Code, rec.Body)
		}
		var out []exchange
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		return out
	}

	// First wake runs setup, then display.
	first := decode(post("/simulate/hallway/wake?source=timer&model=reterminal_e1003"))
	if len(first) != 2 || first[0].Response["status"] != float64(200) || first[1].Response["status"] != float64(0) {
		t.Fatalf("got %+v, want setup then display", first)
	}
	if diff := cmp.Diff(render.Target{Name: "hallway", Model: "reterminal_e1003", Rotation: 90}, withoutWake(stub.target)); diff != "" {
		t.Errorf("target (-want +got):\n%s", diff)
	}

	// The image the display response names is served on the admin listener.
	filename := first[1].Response["filename"].(string)
	if rec := get(t, h, "/images/"+filename+".png"); rec.Code != http.StatusOK {
		t.Errorf("got %d for the image, want 200", rec.Code)
	}

	// Later wakes reuse the key.
	second := decode(post("/simulate/hallway/wake?source=button&model=reterminal_e1003"))
	if len(second) != 1 || second[0].Response["refresh_rate"] != float64(300) {
		t.Errorf("got %+v, want one display with refresh_rate 300", second)
	}
}

func TestSimulateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		url  string
		want int
	}{
		{"unknown device", "/simulate/garage/wake?source=timer&model=reterminal_e1001", http.StatusNotFound},
		{"unknown model", "/simulate/hallway/wake?source=timer&model=kindle", http.StatusBadRequest},
		{"bad source", "/simulate/hallway/wake?source=powercycle&model=reterminal_e1001", http.StatusBadRequest},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			h, _ := newHandler(t)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, testCase.url, nil))
			if rec.Code != testCase.want {
				t.Errorf("got %d, want %d", rec.Code, testCase.want)
			}
		})
	}
}

func TestSimulatePage(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	if rec := get(t, h, "/simulate/hallway"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "AA:BB:CC:DD:EE:FF") {
		t.Errorf("got %d, want 200 naming the MAC", rec.Code)
	}
	if rec := get(t, h, "/simulate/garage"); rec.Code != http.StatusNotFound {
		t.Errorf("got %d for an unknown device, want 404", rec.Code)
	}
}

func withoutWake(t render.Target) render.Target {
	t.Wake = nil
	return t
}
