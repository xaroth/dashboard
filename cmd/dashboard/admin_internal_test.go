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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/device"
	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/render"
	"github.com/xaroth/dashboard/storage"
)

var adminNow = time.Unix(1792087200, 0)

type adminConfig struct {
	Fail bool `yaml:"fail"`
}

// adminModule is a module for the admin UI to show; fail makes its updates fail.
type adminModule struct{ fail bool }

func (m *adminModule) Initialize(_ context.Context, cfg *adminConfig) error {
	m.fail = cfg.Fail
	return nil
}

func (*adminModule) Enabled(context.Context) (bool, error) { return true, nil }

func (m *adminModule) Update(context.Context) error {
	if m.fail {
		return errors.New("token refused")
	}
	return nil
}

func adminRegistration(name string, opts ...module.Option) module.Registration {
	opts = append(opts, module.WithUpdateInterval(15*time.Minute))
	return module.Define[adminModule, adminConfig](name, func() *adminConfig { return &adminConfig{} }, opts...)
}

// loginsPage is a shared value with a page, like a login flow.
type loginsPage struct{}

func (loginsPage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "logins at ", r.URL.Path)
}

var logins = module.DefineShared("logins", func() *struct{} { return &struct{}{} },
	func(context.Context, *struct{}, []struct{}) (loginsPage, error) { return loginsPage{}, nil })

// fixture is the admin UI on a real pipeline, devices and module set: modules
// "sun", which uses the shared logins page, and "car", whose updates fail,
// and pages from a fake renderer.
type fixture struct {
	handler  http.Handler
	renderer *render.FakeRenderer
	db       *storage.DB
}

// newFixture serves devices, or a hallway device when nil, with the page
// e1001/hello. opts set up the renderer further.
func newFixture(tb testing.TB, devices []device.Config, opts ...render.FakeRendererOption) *fixture {
	tb.Helper()
	if devices == nil {
		devices = []device.Config{{Name: "hallway", MAC: "AA:BB:CC:DD:EE:FF", Pages: []string{"e1001/hello"}, Rotation: 90}}
	}
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	now := func() time.Time { return adminNow }

	var configs map[string]yaml.Node
	if err := yaml.Unmarshal([]byte("car: {fail: true}"), &configs); err != nil {
		tb.Fatalf("module config: %v", err)
	}
	set, err := module.Load(tb.Context(), module.Options{
		Registrations: []module.Registration{adminRegistration("sun", module.WithShared(logins)), adminRegistration("car")},
		Configs:       configs,
		Migrator:      db,
		Writer:        db.Writer,
		Reader:        db.Reader,
		Log:           slog.New(slog.DiscardHandler),
		HTTPClient:    http.DefaultClient,
		Now:           now,
	})
	if err != nil {
		tb.Fatalf("modules: %v", err)
	}
	renderer := render.NewFakeRenderer(tb, append([]render.FakeRendererOption{render.ExpectPages(render.PageInfo{Name: "e1001/hello"})}, opts...)...)
	pipeline := render.NewPipeline(renderer, resolver{set: set, loc: time.UTC}, time.UTC, now)

	deviceOpts := device.Options{
		Devices:   devices,
		Pipeline:  pipeline,
		Store:     &device.Store{Max: 4},
		DB:        db.Writer,
		Migrator:  db,
		PublicURL: "http://dash",
		Schedule:  device.Schedule{First: 7 * time.Hour, Battery: time.Hour, USB: 5 * time.Minute, Detail: 5 * time.Minute, Location: time.UTC},
		Log:       slog.New(slog.DiscardHandler),
		Now:       now,
	}
	devs, err := device.Load(tb.Context(), deviceOpts)
	if err != nil {
		tb.Fatalf("devices: %v", err)
	}
	deviceOpts.Store = &device.Store{Max: 4}
	sim, err := device.Load(tb.Context(), deviceOpts)
	if err != nil {
		tb.Fatalf("simulator: %v", err)
	}
	simulator := http.NewServeMux()
	sim.Register(simulator)
	h := &adminHandler{
		Pipeline:  pipeline,
		Devices:   devs,
		Modules:   set,
		Simulator: simulator,
		Log:       slog.New(slog.DiscardHandler),
	}
	mux := http.NewServeMux()
	h.Register(mux)
	set.RegisterAdmin(mux)
	return &fixture{handler: mux, renderer: renderer, db: db}
}

func get(tb testing.TB, h http.Handler, url string) *httptest.ResponseRecorder {
	tb.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

func post(tb testing.TB, h http.Handler, url string) *httptest.ResponseRecorder {
	tb.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, url, nil))
	return rec
}

func TestIndex(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	for _, name := range []string{"sun", "car"} {
		post(t, f.handler, "/modules/"+name+"/update")
	}
	rec := get(t, f.handler, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	for _, want := range []string{
		`src="/preview/hallway/e1001/hello.png"`,
		`href="/preview/hallway/e1001/hello/props.json"`,
		`src="/preview/page/e1001/hello.png?model=reterminal_e1003&rotation=90"`,
		"<td>sun</td>\n    <td>waiting</td>",
		"<td>car</td>\n    <td>waiting <span class=\"stale\">stale</span></td>",
		`<td>15m0s</td>`,
		`<td>token refused</td>`,
		`<a href="/logins/">logins</a>`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("got index without %s, want it", want)
		}
	}
}

func TestSharedPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	if rec := get(t, f.handler, "/logins/login"); rec.Code != http.StatusOK || rec.Body.String() != "logins at /login" {
		t.Errorf("got %d %q, want 200 \"logins at /login\"", rec.Code, rec.Body)
	}
}

func TestIndexWithoutDevices(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []device.Config{})
	rec := get(t, f.handler, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No devices configured") {
		t.Errorf("got %d, want 200 with \"No devices configured\"", rec.Code)
	}
}

func TestIndexErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		renderer []render.FakeRendererOption
		closeDB  bool
	}{
		{"pages", []render.FakeRendererOption{render.ExpectDescribe_Error(errors.New("bundle broken"))}, false},
		{"module statuses", nil, true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil, testCase.renderer...)
			if testCase.closeDB {
				f.db.Close()
			}
			if rec := get(t, f.handler, "/"); rec.Code != http.StatusInternalServerError {
				t.Errorf("got %d %s, want 500", rec.Code, rec.Body)
			}
		})
	}
}

func TestUpdateModule(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		module string
		want   int
	}{
		{"succeeds", "sun", http.StatusSeeOther},
		{"fails", "car", http.StatusSeeOther}, // the failure shows in the table
		{"unknown", "nope", http.StatusNotFound},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			if rec := post(t, f.handler, "/modules/"+testCase.module+"/update"); rec.Code != testCase.want {
				t.Errorf("got %d, want %d", rec.Code, testCase.want)
			}
		})
	}
}

func TestPreviewPNG(t *testing.T) {
	t.Parallel()
	const textType = "text/plain; charset=utf-8"
	testCases := []struct {
		name       string
		url        string
		renderer   []render.FakeRendererOption
		wantStatus int
		wantType   string
		wantDevice render.DeviceProps // as rendered; zero when nothing was
	}{
		{"device", "/preview/hallway/e1001/hello.png", nil, http.StatusOK, "image/png", render.DeviceProps{Name: "hallway", Model: "reterminal_e1001", Rotation: 90}},
		{"device with model", "/preview/hallway/e1001/hello.png?model=reterminal_e1003", nil, http.StatusOK, "image/png", render.DeviceProps{Name: "hallway", Model: "reterminal_e1003", Rotation: 90}},
		{"panel defaults", "/preview/page/e1001/hello.png", nil, http.StatusOK, "image/png", render.DeviceProps{Name: "preview", Model: "reterminal_e1001"}},
		{"panel", "/preview/page/e1001/hello.png?model=reterminal_e1003&rotation=270", nil, http.StatusOK, "image/png", render.DeviceProps{Name: "preview", Model: "reterminal_e1003", Rotation: 270}},
		{"unknown device", "/preview/garage/e1001/hello.png", nil, http.StatusNotFound, textType, render.DeviceProps{}},
		{"not png", "/preview/hallway/e1001/hello.jpg", nil, http.StatusNotFound, textType, render.DeviceProps{}},
		{"bad rotation", "/preview/page/e1001/hello.png?rotation=45", nil, http.StatusBadRequest, textType, render.DeviceProps{}},
		{"unknown page", "/preview/page/e1001/nope.png", nil, http.StatusNotFound, textType, render.DeviceProps{}},
		{"unknown model", "/preview/hallway/e1001/hello.png?model=kindle", nil, http.StatusBadRequest, textType, render.DeviceProps{}},
		{"render error", "/preview/page/e1001/hello.png", []render.FakeRendererOption{render.ExpectRender_Error(errors.New("boom"))}, http.StatusInternalServerError, textType, render.DeviceProps{}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil, testCase.renderer...)
			rec := get(t, f.handler, testCase.url)
			if rec.Code != testCase.wantStatus {
				t.Fatalf("got %d %s, want %d", rec.Code, rec.Body, testCase.wantStatus)
			}
			if got := rec.Header().Get("Content-Type"); got != testCase.wantType {
				t.Errorf("got content type %q, want %q", got, testCase.wantType)
			}
			if diff := cmp.Diff(testCase.wantDevice, f.renderer.LastProps().Device); diff != "" {
				t.Errorf("rendered device (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPreviewProps(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		url        string
		renderer   []render.FakeRendererOption
		wantStatus int
		want       *render.Props
	}{
		{"device", "/preview/hallway/e1001/hello/props.json", nil, http.StatusOK, &render.Props{
			Data:     map[string]render.Result{},
			Device:   render.DeviceProps{Name: "hallway", Model: "reterminal_e1001", Rotation: 90},
			Viewport: render.Viewport{Width: 480, Height: 800, Portrait: true},
			System:   render.SystemProps{StaleModules: []string{"car", "sun"}},
			Now:      adminNow.UTC().Format(time.RFC3339),
			Timezone: "UTC",
		}},
		{"unknown device", "/preview/garage/e1001/hello/props.json", nil, http.StatusNotFound, nil},
		{"unknown page", "/preview/hallway/e1001/nope/props.json", nil, http.StatusNotFound, nil},
		{"pages error", "/preview/hallway/e1001/hello/props.json", []render.FakeRendererOption{render.ExpectDescribe_Error(errors.New("bundle broken"))}, http.StatusInternalServerError, nil},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil, testCase.renderer...)
			rec := get(t, f.handler, testCase.url)
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
	f := newFixture(t, nil)

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
	first := decode(post(t, f.handler, "/simulate/hallway/wake?source=timer&model=reterminal_e1003"))
	if len(first) != 2 || first[0].Response["status"] != float64(200) || first[1].Response["status"] != float64(0) {
		t.Fatalf("got %+v, want setup then display", first)
	}
	got := f.renderer.LastProps().Device
	if got.Wake == nil {
		t.Error("got no wake in the rendered props, want the simulated one")
	}
	got.Wake = nil
	if diff := cmp.Diff(render.DeviceProps{Name: "hallway", Model: "reterminal_e1003", Rotation: 90}, got); diff != "" {
		t.Errorf("rendered device (-want +got):\n%s", diff)
	}

	// The image the display response names is served on the admin listener.
	filename := first[1].Response["filename"].(string)
	if rec := get(t, f.handler, "/images/"+filename+".png"); rec.Code != http.StatusOK {
		t.Errorf("got %d for the image, want 200", rec.Code)
	}

	// Later wakes reuse the key.
	second := decode(post(t, f.handler, "/simulate/hallway/wake?source=button&model=reterminal_e1003"))
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
			f := newFixture(t, nil)
			if rec := post(t, f.handler, testCase.url); rec.Code != testCase.want {
				t.Errorf("got %d, want %d", rec.Code, testCase.want)
			}
		})
	}
}

func TestSimulatePage(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		url        string
		wantStatus int
		wantBody   string
	}{
		{"known device", "/simulate/hallway", http.StatusOK, "AA:BB:CC:DD:EE:FF"},
		{"unknown device", "/simulate/garage", http.StatusNotFound, ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			rec := get(t, f.handler, testCase.url)
			if rec.Code != testCase.wantStatus || !strings.Contains(rec.Body.String(), testCase.wantBody) {
				t.Errorf("got %d %s, want %d containing %q", rec.Code, rec.Body, testCase.wantStatus, testCase.wantBody)
			}
		})
	}
}
