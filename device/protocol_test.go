package device_test

import (
	"context"
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

	"github.com/xaroth/dashboard/device"
	"github.com/xaroth/dashboard/render"
	"github.com/xaroth/dashboard/screen"
	"github.com/xaroth/dashboard/storage"
)

const (
	mac   = "AA:BB:CC:DD:EE:FF"
	other = "11:22:33:44:55:66"
)

type env struct {
	mux     *http.ServeMux
	devices *device.Devices
	store   *device.Store
	fake    *render.FakeRenderer
	clock   *atomic.Int64
	db      *storage.DB
}

// hourly wakes on the hour from 07:00 up to and including 00:00.
func hourly(loc *time.Location) device.Schedule {
	return device.Schedule{First: 7 * time.Hour, Last: 0, Battery: time.Hour, USB: 5 * time.Minute, Detail: 5 * time.Minute, Location: loc}
}

func newEnv(tb testing.TB, opts ...func(*device.Options)) *env {
	tb.Helper()
	return newEnvSchedule(tb, hourly(time.UTC), opts...)
}

func newEnvSchedule(tb testing.TB, schedule device.Schedule, opts ...func(*device.Options)) *env {
	tb.Helper()
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	return loadEnv(tb, db, schedule, opts...)
}

// loadEnv loads the devices on db, as a service start does. opts adjust
// the options before the load.
func loadEnv(tb testing.TB, db *storage.DB, schedule device.Schedule, opts ...func(*device.Options)) *env {
	tb.Helper()
	e, o := envOptions(tb, db, schedule, opts...)
	devices, err := device.Load(tb.Context(), o)
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	e.devices = devices
	devices.Register(e.mux)
	return e
}

// envOptions returns an env without devices and the options to load them with.
func envOptions(tb testing.TB, db *storage.DB, schedule device.Schedule, opts ...func(*device.Options)) (*env, device.Options) {
	tb.Helper()
	fake := render.NewFakeRenderer(tb, render.ExpectPages(render.PageInfo{Name: "main"}, render.PageInfo{Name: "detail"}))
	clock := &atomic.Int64{}
	clock.Store(time.Unix(1792087200, 0).UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()) }
	store := &device.Store{Max: 4}
	o := device.Options{
		Devices:   []device.Config{{Name: "hallway", MAC: mac, Rotation: 0, Pages: []string{"main", "detail"}}},
		Pipeline:  render.NewPipeline(fake, render.NewFakeResolver(tb), time.UTC, now),
		Store:     store,
		DB:        db.Writer,
		Migrator:  db,
		PublicURL: "http://dash:8080/",
		Schedule:  schedule,
		Log:       slog.New(slog.DiscardHandler),
		Now:       now,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &env{mux: http.NewServeMux(), store: store, fake: fake, clock: clock, db: db}, o
}

func TestLoadRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		adjust func(*device.Options)
	}{
		{"zero battery interval", func(o *device.Options) { o.Schedule.Battery = 0 }},
		{"zero usb interval", func(o *device.Options) { o.Schedule.USB = 0 }},
		{"negative detail", func(o *device.Options) { o.Schedule.Detail = -time.Minute }},
		{"no pages", func(o *device.Options) { o.Devices[0].Pages = nil }},
		{"unknown power", func(o *device.Options) { o.Devices[0].Power = "solar" }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			db, err := storage.Open(t.Context(), t.TempDir())
			if err != nil {
				t.Fatalf("storage: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			_, o := envOptions(t, db, hourly(time.UTC), testCase.adjust)
			if _, err := device.Load(t.Context(), o); err == nil {
				t.Error("got no error, want one")
			}
		})
	}
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
			if key, ok := body["api_key"].(string); testCase.want["status"] == float64(200) && len(key) != 26 {
				t.Errorf("got api_key %q (%v), want 26 chars", key, ok)
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
		"refresh_rate":     float64(3600),
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
		t.Fatalf("image: got %d %q, want 200 image/png", rec.Code, rec.Header().Get("Content-Type"))
	}
	cfg, err := png.DecodeConfig(rec.Body)
	if err != nil || cfg.Width != 1872 || cfg.Height != 1404 {
		t.Errorf("image: got %dx%d (%v), want 1872x1404", cfg.Width, cfg.Height, err)
	}

	// The wake is recorded and used for later renders.
	target, _ := e.devices.Target("hallway")
	battery, rssi := 4.05, -61
	wantTarget := screen.Target{Name: "hallway", Model: "reterminal_e1003", Wake: &screen.Wake{
		Source: "timer", Battery: &battery, RSSI: &rssi, Firmware: "1.8.7", At: time.Unix(1792087200, 0), PowerSource: screen.PowerSourceDefault,
	}}
	if diff := cmp.Diff(wantTarget, target); diff != "" {
		t.Errorf("target (-want +got):\n%s", diff)
	}
}

func TestDisplayPages(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	// Sequential: each wake depends on the one before.
	steps := []struct {
		source      string
		wantPage    string
		wantRefresh float64
	}{
		{"timer", "main", 3600},
		{"button", "detail", 300},
		{"EXT0", "main", 300}, // wraps around
		{"button", "detail", 300},
		{"powercycle", "main", 3600},
	}
	for i, step := range steps {
		_, body := e.display(t, key, step.source)
		if body["refresh_rate"] != step.wantRefresh {
			t.Errorf("step %d (%s): got refresh_rate %v, want %v", i, step.source, body["refresh_rate"], step.wantRefresh)
		}
		if got := e.devices.Statuses()[0].Page; got != step.wantPage {
			t.Errorf("step %d (%s): got page %q, want %q", i, step.source, got, step.wantPage)
		}
	}
}

func TestDisplayRefresh(t *testing.T) {
	t.Parallel()
	amsterdam, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("loading location: %v", err)
	}
	testCases := []struct {
		name     string
		schedule device.Schedule
		at       string // local time, time.DateTime with optional fraction
		source   string
		want     float64
	}{
		{"on the hour", hourly(amsterdam), "2026-10-15 18:00:00", "timer", 3600},
		{"just before the hour", hourly(amsterdam), "2026-10-15 17:59:30", "timer", 3630},
		{"last wake of the day", hourly(amsterdam), "2026-10-15 23:30:00", "timer", 1800},
		{"night sleep", hourly(amsterdam), "2026-10-16 00:00:00", "timer", 7 * 3600},
		{"woken at night", hourly(amsterdam), "2026-10-16 03:00:00", "powercycle", 4 * 3600},
		{"night sleep on DST end", hourly(amsterdam), "2026-10-25 00:00:00", "timer", 8 * 3600},
		{"night sleep on DST start", hourly(amsterdam), "2027-03-28 00:00:00", "timer", 6 * 3600},
		{"exactly a minute before the hour", hourly(amsterdam), "2026-10-15 17:59:00", "timer", 3660},
		{"window past midnight", device.Schedule{First: 7 * time.Hour, Last: 2 * time.Hour, Battery: time.Hour, USB: 5 * time.Minute, Detail: 5 * time.Minute, Location: amsterdam}, "2026-10-16 01:00:00", "timer", 3600},
		{"once a day", device.Schedule{First: 7 * time.Hour, Last: 7 * time.Hour, Battery: 24 * time.Hour, USB: 5 * time.Minute, Detail: 5 * time.Minute, Location: amsterdam}, "2026-10-15 07:00:00", "timer", 24 * 3600},
		{"detail rounds up", hourly(amsterdam), "2026-10-15 18:59:59.6", "button", 1},
		{"detail", hourly(amsterdam), "2026-10-15 18:30:00", "button", 300},
		{"detail cut short by wake", hourly(amsterdam), "2026-10-15 18:58:00", "button", 120},
		{"half-hourly", device.Schedule{First: 7 * time.Hour, Last: 22 * time.Hour, Battery: 30 * time.Minute, USB: 5 * time.Minute, Detail: 5 * time.Minute, Location: amsterdam}, "2026-10-15 18:00:00", "timer", 1800},
		{"two-hourly skips past last", device.Schedule{First: 7 * time.Hour, Last: 22 * time.Hour, Battery: 2 * time.Hour, USB: 5 * time.Minute, Detail: 5 * time.Minute, Location: amsterdam}, "2026-10-15 21:00:00", "timer", 10 * 3600},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			at, err := time.ParseInLocation("2006-01-02 15:04:05.999", testCase.at, amsterdam)
			if err != nil {
				t.Fatalf("parsing time: %v", err)
			}
			e := newEnvSchedule(t, testCase.schedule)
			e.clock.Store(at.UnixNano())
			_, body := e.display(t, e.setup(t), testCase.source)
			if body["refresh_rate"] != testCase.want {
				t.Errorf("got refresh_rate %v, want %v", body["refresh_rate"], testCase.want)
			}
		})
	}
}

// slowPipeline advances the clock by took on every render.
type slowPipeline struct {
	device.Pipeline
	clock *atomic.Int64
	took  time.Duration
}

func (p *slowPipeline) Render(ctx context.Context, t screen.Target, page string) (screen.Image, error) {
	p.clock.Add(int64(p.took))
	return p.Pipeline.Render(ctx, t, page)
}

func TestDisplayRefreshAfterRender(t *testing.T) {
	t.Parallel()
	slow := &slowPipeline{took: 40 * time.Second}
	e := newEnv(t, func(o *device.Options) {
		slow.Pipeline = o.Pipeline
		o.Pipeline = slow
	})
	slow.clock = e.clock
	key := e.setup(t)
	e.clock.Store(time.Date(2026, 10, 15, 17, 59, 30, 0, time.UTC).UnixNano())
	// Rendered at 18:00:10: the next wake past a minute is 19:00.
	if _, body := e.display(t, key, "timer"); body["refresh_rate"] != float64(3590) {
		t.Errorf("got refresh_rate %v, want 3590", body["refresh_rate"])
	}
}

func TestDisplayPower(t *testing.T) {
	t.Parallel()
	amsterdam, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("loading location: %v", err)
	}
	testCases := []struct {
		name       string
		power      device.Power
		header     string // USB-Connected, if set
		at         string // local time
		wantUSB    bool
		wantSource screen.PowerSource
		want       float64
	}{
		// At 18:00 the USB interval's next wake is 18:05, the battery one's 19:00.
		{"no header", device.PowerAuto, "", "2026-10-15 18:00:00", false, screen.PowerSourceDefault, 3600},
		{"header usb", device.PowerAuto, "true", "2026-10-15 18:00:00", true, screen.PowerSourceHeader, 300},
		{"header battery", "", "false", "2026-10-15 18:00:00", false, screen.PowerSourceHeader, 3600},
		{"bad header", device.PowerAuto, "maybe", "2026-10-15 18:00:00", false, screen.PowerSourceDefault, 3600},
		{"config usb", device.PowerUSB, "", "2026-10-15 18:00:00", true, screen.PowerSourceConfig, 300},
		{"config battery over header", device.PowerBattery, "true", "2026-10-15 18:00:00", false, screen.PowerSourceConfig, 3600},
		{"usb before last wake", device.PowerUSB, "", "2026-10-15 23:57:00", true, screen.PowerSourceConfig, 180},
		{"usb night sleep", device.PowerUSB, "", "2026-10-16 00:00:00", true, screen.PowerSourceConfig, 7 * 3600},
		{"usb night sleep on DST end", device.PowerUSB, "", "2026-10-25 00:00:00", true, screen.PowerSourceConfig, 8 * 3600},
		{"usb night sleep on DST start", device.PowerUSB, "", "2027-03-28 00:00:00", true, screen.PowerSourceConfig, 6 * 3600},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			at, err := time.ParseInLocation(time.DateTime, testCase.at, amsterdam)
			if err != nil {
				t.Fatalf("parsing time: %v", err)
			}
			e := newEnvSchedule(t, hourly(amsterdam), func(o *device.Options) { o.Devices[0].Power = testCase.power })
			e.clock.Store(at.UnixNano())
			headers := map[string]string{"ID": mac, "Access-Token": e.setup(t), "Update-Source": "timer"}
			if testCase.header != "" {
				headers["USB-Connected"] = testCase.header
			}
			_, body := e.do(t, http.MethodGet, "/api/display", headers)
			if body["refresh_rate"] != testCase.want {
				t.Errorf("got refresh_rate %v, want %v", body["refresh_rate"], testCase.want)
			}
			w := e.devices.Statuses()[0].Wake
			if w.USB != testCase.wantUSB || w.PowerSource != testCase.wantSource {
				t.Errorf("got usb %v from %q, want %v from %q", w.USB, w.PowerSource, testCase.wantUSB, testCase.wantSource)
			}
		})
	}
}

func TestDisplayUnchangedImageKeepsFilename(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	_, first := e.display(t, key, "timer")
	e.clock.Add(int64(time.Hour))
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

// setupKey stands for the api key setup returned.
const setupKey = "<setup key>"

func TestDisplayHeaders(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		setup     bool
		id        string
		token     string
		model     string
		wantCode  int
		wantModel string
	}{
		{"unknown model", true, mac, setupKey, "kindle", http.StatusOK, device.DefaultModel},
		{"wrong token", true, mac, "nope", "", http.StatusUnauthorized, device.DefaultModel},
		{"unknown device", true, other, "x", "", http.StatusNotFound, device.DefaultModel},
		{"no token", true, mac, "", "", http.StatusNotFound, device.DefaultModel},
		// No key stored for the MAC: a presented token is not adopted.
		{"no stored key", false, mac, "kept-from-before", "", http.StatusUnauthorized, device.DefaultModel},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			token := testCase.token
			if testCase.setup {
				if key := e.setup(t); token == setupKey {
					token = key
				}
			}
			code, body := e.do(t, http.MethodGet, "/api/display", map[string]string{"ID": testCase.id, "Access-Token": token, "Model": testCase.model})
			if code != testCase.wantCode {
				t.Errorf("got %d %v, want %d", code, body, testCase.wantCode)
			}
			if body["status"] == float64(500) {
				t.Error("got JSON status 500, want any other: 500 resets the firmware")
			}
			if target, _ := e.devices.Target("hallway"); target.Model != testCase.wantModel {
				t.Errorf("got model %q, want %q", target.Model, testCase.wantModel)
			}
		})
	}
}

func TestKeySurvivesRestart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	restarted := loadEnv(t, e.db, hourly(time.UTC))
	if code, body := restarted.display(t, key, "timer"); code != http.StatusOK {
		t.Errorf("got %d %v with the key from before the restart, want 200", code, body)
	}
	if got := restarted.setup(t); got != key {
		t.Errorf("got key %q from setup after the restart, want %q", got, key)
	}
	if code, _ := restarted.display(t, "other", "timer"); code != http.StatusUnauthorized {
		t.Errorf("got %d for another token, want 401", code)
	}
}

func TestKeyCached(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	key := e.setup(t)
	// Wakes after setup do not touch the database.
	e.db.Writer.Close()
	if code, body := e.display(t, key, "timer"); code != http.StatusOK {
		t.Errorf("display: got %d %v, want 200", code, body)
	}
	if code, _ := e.do(t, http.MethodPost, "/api/log", map[string]string{"ID": mac, "Access-Token": key}); code != http.StatusNoContent {
		t.Errorf("log: got %d, want 204", code)
	}
	if code, _ := e.display(t, "other", "timer"); code != http.StatusUnauthorized {
		t.Errorf("display with another token: got %d, want 401", code)
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
		e.store.Put(fmt.Sprintf("other-%d", i), []byte("x"))
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
	testCases := []struct {
		name  string
		token string
		want  int
	}{
		{"accepted", setupKey, http.StatusNoContent},
		{"wrong token", "nope", http.StatusUnauthorized},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			token := testCase.token
			if key := e.setup(t); token == setupKey {
				token = key
			}
			if code, _ := e.do(t, http.MethodPost, "/api/log", map[string]string{"ID": mac, "Access-Token": token}); code != testCase.want {
				t.Errorf("got %d, want %d", code, testCase.want)
			}
		})
	}
}

func TestImageNotFound(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		path string
	}{
		{"not stored", "/images/000000000000-1792087200.png"},
		{"not png", "/images/x.jpg"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			if code, _ := e.do(t, http.MethodGet, testCase.path, nil); code != http.StatusNotFound {
				t.Errorf("got %d, want 404", code)
			}
		})
	}
}

func TestNormalizeMAC(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"lower case", "aa:bb:cc:dd:ee:ff", "AA:BB:CC:DD:EE:FF", false},
		{"garbage", "nope", "", true},
		{"eui-64", "aa:bb:cc:dd:ee:ff:00:11", "", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := device.NormalizeMAC(testCase.in)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("got error %v, want error %v", err, testCase.wantErr)
			}
			if got != testCase.want {
				t.Errorf("got %q, want %q", got, testCase.want)
			}
		})
	}
}
