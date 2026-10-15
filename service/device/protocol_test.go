package device_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xaroth/dashboard/service/device"
	"github.com/xaroth/dashboard/service/render"
)

const (
	mac   = "AA:BB:CC:DD:EE:FF"
	other = "11:22:33:44:55:66"
)

type env struct {
	mux     *http.ServeMux
	devices *device.Devices
	fake    *render.FakeRenderer
	clock   *atomic.Int64
}

func newEnv(tb testing.TB) *env {
	tb.Helper()
	fake := render.NewFakeRenderer(render.PageInfo{Name: "main"}, render.PageInfo{Name: "detail"})
	clock := &atomic.Int64{}
	clock.Store(1792087200)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	devices := device.New(device.Options{
		Devices:   []device.Config{{Name: "hallway", MAC: mac, Rotation: 0, Pages: []string{"main", "detail"}}},
		Pipeline:  render.NewPipeline(fake, render.NewFakeResolver(nil), time.UTC, now),
		Store:     &device.Store{Max: 4},
		PublicURL: "http://dash:8080/",
		Log:       slog.New(slog.DiscardHandler),
		Now:       now,
	})
	mux := http.NewServeMux()
	devices.Register(mux)
	return &env{mux: mux, devices: devices, fake: fake, clock: clock}
}

func (e *env) do(tb testing.TB, method, path string, headers map[string]string) (int, map[string]any) {
	tb.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// setup registers the device and returns its api key.
func (e *env) setup(tb testing.TB) string {
	tb.Helper()
	code, body := e.do(tb, http.MethodGet, "/api/setup", map[string]string{"ID": mac})
	if code != http.StatusOK || body["status"] != float64(200) {
		tb.Fatalf("setup: got %d %v", code, body)
	}
	return body["api_key"].(string)
}

func (e *env) display(tb testing.TB, key, source string) (int, map[string]any) {
	tb.Helper()
	return e.do(tb, http.MethodGet, "/api/display", map[string]string{
		"ID":              mac,
		"Access-Token":    key,
		"Model":           "reterminal_e1003",
		"Battery-Voltage": "4.05",
		"RSSI":            "-61",
		"FW-Version":      "1.8.7",
		"Update-Source":   source,
	})
}

func TestSetup(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		id      string
		want    map[string]any
		pending []string
	}{
		{"known", "aa:bb:cc:dd:ee:ff", map[string]any{"status": float64(200), "friendly_id": "DDEEFF", "message": "Registered as hallway"}, []string{}},
		{"unknown", other, map[string]any{"status": float64(404), "message": "MAC not registered"}, []string{other}},
		{"missing ID", "", map[string]any{"status": float64(404), "message": "missing or invalid ID header"}, []string{}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			code, body := e.do(t, http.MethodGet, "/api/setup", map[string]string{"ID": testCase.id})
			if code != http.StatusOK {
				t.Errorf("got HTTP %d, want 200", code)
			}
			if key, ok := body["api_key"].(string); testCase.want["status"] == float64(200) && len(key) != 32 {
				t.Errorf("got api_key %q (%v), want 32 hex", key, ok)
			}
			if diff := cmp.Diff(testCase.want, body, cmpopts.IgnoreMapEntries(func(k string, _ any) bool { return k == "api_key" })); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
			var pending []string
			for _, p := range e.devices.Pending() {
				pending = append(pending, p.MAC)
			}
			if diff := cmp.Diff(testCase.pending, pending, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("pending (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSetupKeepsKey(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if first, second := e.setup(t), e.setup(t); first != second {
		t.Errorf("got keys %q then %q, want the same", first, second)
	}
}

func TestDisplay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	code, body := e.display(t, key, "timer")
	if code != http.StatusOK {
		t.Fatalf("got HTTP %d %v, want 200", code, body)
	}
	filename, _ := body["filename"].(string)
	want := map[string]any{
		"status":           float64(0),
		"image_url":        "http://dash:8080/images/" + filename + ".png",
		"filename":         filename,
		"refresh_rate":     float64(900),
		"reset_firmware":   false,
		"update_firmware":  false,
		"firmware_url":     nil,
		"special_function": "sleep",
	}
	if diff := cmp.Diff(want, body); diff != "" {
		t.Errorf("body (-want +got):\n%s", diff)
	}
	if len(filename) != 23 || !strings.HasSuffix(filename, "-1792087200") {
		t.Errorf("got filename %q, want 12 hex + -1792087200", filename)
	}

	// The image is served, at the panel size the device reported.
	req := httptest.NewRequest(http.MethodGet, "/images/"+filename+".png", nil)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("image: got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	cfg, err := png.DecodeConfig(rec.Body)
	if err != nil || cfg.Width != 1872 || cfg.Height != 1404 {
		t.Errorf("image: got %dx%d (%v), want 1872x1404", cfg.Width, cfg.Height, err)
	}

	// The wake is recorded and used for later renders.
	target, _ := e.devices.Target("hallway")
	battery, rssi := 4.05, -61
	wantTarget := render.Target{Name: "hallway", Model: "reterminal_e1003", Wake: &render.Wake{
		Source: "timer", Battery: &battery, RSSI: &rssi, Firmware: "1.8.7", At: time.Unix(1792087200, 0),
	}}
	if diff := cmp.Diff(wantTarget, target); diff != "" {
		t.Errorf("target (-want +got):\n%s", diff)
	}
}

func TestDisplayPages(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	testCases := []struct {
		source      string
		wantPage    string
		wantRefresh float64
	}{
		{"timer", "main", 900},
		{"button", "detail", 300},
		{"EXT0", "main", 300}, // wraps around
		{"button", "detail", 300},
		{"powercycle", "main", 900},
	}
	// Sequential: each wake depends on the one before.
	for _, testCase := range testCases {
		_, body := e.display(t, key, testCase.source)
		if body["refresh_rate"] != testCase.wantRefresh {
			t.Errorf("%s: got refresh_rate %v, want %v", testCase.source, body["refresh_rate"], testCase.wantRefresh)
		}
		if got := e.devices.Statuses()[0].Page; got != testCase.wantPage {
			t.Errorf("%s: got page %q, want %q", testCase.source, got, testCase.wantPage)
		}
	}
}

func TestDisplayUnchangedImageKeepsFilename(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	_, first := e.display(t, key, "timer")
	e.clock.Add(3600)
	_, second := e.display(t, key, "timer")
	if first["filename"] != second["filename"] {
		t.Errorf("got %v then %v, want the same filename for the same pixels", first["filename"], second["filename"])
	}
}

func TestDisplayRenderFailure(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	e.fake.FailRenders(errors.New("boom"))
	if code, _ := e.display(t, key, "timer"); code != http.StatusServiceUnavailable {
		t.Errorf("without a previous image: got HTTP %d, want 503", code)
	}

	e.fake.FailRenders(nil)
	_, ok := e.display(t, key, "timer")
	e.fake.FailRenders(errors.New("boom"))
	code, failed := e.display(t, key, "timer")
	if code != http.StatusOK || failed["filename"] != ok["filename"] || failed["status"] != float64(0) {
		t.Errorf("with a previous image: got %d %v, want 200 with filename %v", code, failed, ok["filename"])
	}
}

func TestDisplayAuth(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		setup    bool
		id       string
		token    string
		wantCode int
	}{
		{"wrong token", true, mac, "nope", http.StatusUnauthorized},
		{"unknown device", true, other, "x", http.StatusNotFound},
		{"no token", true, mac, "", http.StatusNotFound},
		// After a restart the key is gone; the first token presented is adopted.
		{"token adopted after restart", false, mac, "kept-from-before", http.StatusOK},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			if testCase.setup {
				e.setup(t)
			}
			code, body := e.do(t, http.MethodGet, "/api/display", map[string]string{"ID": testCase.id, "Access-Token": testCase.token})
			if code != testCase.wantCode {
				t.Errorf("got %d %v, want %d", code, body, testCase.wantCode)
			}
			if body["status"] == float64(500) {
				t.Error("got JSON status 500, which resets the firmware")
			}
		})
	}
}

func TestDisplayAdoptedTokenIsBinding(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if code, _ := e.display(t, "first", "timer"); code != http.StatusOK {
		t.Fatalf("got %d adopting a token, want 200", code)
	}
	if code, _ := e.display(t, "second", "timer"); code != http.StatusUnauthorized {
		t.Errorf("got %d for a different token, want 401", code)
	}
}

func TestDisplayIgnoresUnknownModel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	code, _ := e.do(t, http.MethodGet, "/api/display", map[string]string{"ID": mac, "Access-Token": key, "Model": "kindle"})
	if code != http.StatusOK {
		t.Fatalf("got %d, want 200", code)
	}
	if target, _ := e.devices.Target("hallway"); target.Model != device.DefaultModel {
		t.Errorf("got model %q, want %q", target.Model, device.DefaultModel)
	}
}

func TestSimulatorIsSeparate(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	sim := map[string]string{"ID": mac, device.SimulatorHeader: "1"}
	_, setup := e.do(t, http.MethodGet, "/api/setup", sim)
	sim["Access-Token"] = setup["api_key"].(string)
	sim["Model"] = "reterminal_e1003"
	sim["Update-Source"] = "button"
	if code, _ := e.do(t, http.MethodGet, "/api/display", sim); code != http.StatusOK {
		t.Fatalf("simulated display: got %d, want 200", code)
	}

	// The real device, restarted service: its old token is still adopted and its state is untouched.
	if target, _ := e.devices.Target("hallway"); target.Wake != nil || target.Model != device.DefaultModel {
		t.Errorf("got real target %+v after a simulated wake, want untouched", target)
	}
	if code, _ := e.display(t, "kept-from-before", "timer"); code != http.StatusOK {
		t.Errorf("real device with its old token: got %d, want 200", code)
	}
}

func TestDisplayReputsEvictedImage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	_, first := e.display(t, key, "timer")
	filename := first["filename"].(string)
	// Evict it: the store keeps 4 images.
	for i := range 4 {
		e.devices.Store().Put(fmt.Sprintf("other-%d", i), []byte("x"))
	}
	if code, _ := e.do(t, http.MethodGet, "/images/"+filename+".png", nil); code != http.StatusNotFound {
		t.Fatalf("got %d, want the image evicted", code)
	}
	_, second := e.display(t, key, "timer")
	if second["filename"] != filename {
		t.Fatalf("got %v, want the unchanged filename %s", second["filename"], filename)
	}
	if code, _ := e.do(t, http.MethodGet, "/images/"+filename+".png", nil); code != http.StatusOK {
		t.Errorf("got %d after the next wake, want the image back", code)
	}
}

func TestSetupPendingIsCapped(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for i := range 100 {
		e.do(t, http.MethodGet, "/api/setup", map[string]string{"ID": fmt.Sprintf("02:00:00:00:00:%02x", i)})
	}
	if got := len(e.devices.Pending()); got > 32 {
		t.Errorf("got %d pending, want at most 32", got)
	}
}

func TestLog(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	if code, _ := e.do(t, http.MethodPost, "/api/log", map[string]string{"ID": mac, "Access-Token": key}); code != http.StatusNoContent {
		t.Errorf("got %d, want 204", code)
	}
	if code, _ := e.do(t, http.MethodPost, "/api/log", map[string]string{"ID": mac, "Access-Token": "nope"}); code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d, want 401", code)
	}
}

func TestImageNotFound(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, path := range []string{"/images/000000000000-1792087200.png", "/images/x.jpg"} {
		if code, _ := e.do(t, http.MethodGet, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, code)
		}
	}
}
