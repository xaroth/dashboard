// package main cannot be imported, so its tests are internal.
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/device"
)

func env(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func TestParseConfig(t *testing.T) {
	t.Parallel()
	raw := `
public_url: http://192.168.1.10:8080
timezone: America/New_York
schedule:
  first_wake: "06:30"
devices:
  hallway:
    mac: "aa-bb-cc-dd-ee-ff"
    rotation: '${ROT}'
    layout: e1001
    pages: [hello]
    power: usb
    params:
      hello.events: {limit: 8}
modules:
  calendar:
    ical_url: ${ICAL}
`
	cfg, err := parseConfig([]byte(raw), env(map[string]string{"ICAL": "https://x/y.ics?a=1#b: c", "ROT": "90"}))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}

	var calendar map[string]string
	node := cfg.Modules["calendar"]
	if err := node.Decode(&calendar); err != nil {
		t.Fatalf("decoding calendar: %v", err)
	}
	if diff := cmp.Diff(map[string]string{"ical_url": "https://x/y.ics?a=1#b: c"}, calendar); diff != "" {
		t.Errorf("calendar (-want +got):\n%s", diff)
	}

	cfg.Modules = nil
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("loading location: %v", err)
	}
	want := Config{
		Listen:    Listen{Device: ":8080", Admin: "127.0.0.1:8081"},
		PublicURL: "http://192.168.1.10:8080",
		Location:  loc,
		DataDir:   "/data",
		Schedule:  device.Schedule{First: 6*time.Hour + 30*time.Minute, Battery: time.Hour, USB: 5 * time.Minute, Detail: 5 * time.Minute, Location: loc},
		Renderer:  RendererConfig{Bun: "bun", Dir: "."},
		Devices: map[string]Device{"hallway": {
			MAC:      "AA:BB:CC:DD:EE:FF",
			Rotation: 90,
			Layout:   "e1001",
			Pages:    []string{"hello"},
			Power:    device.PowerUSB,
			Params:   map[string]map[string]any{"hello.events": {"limit": 8}},
		}},
	}
	if diff := cmp.Diff(want, cfg, cmp.Comparer(sameLocation)); diff != "" {
		t.Errorf("config (-want +got):\n%s", diff)
	}
}

func sameLocation(a, b *time.Location) bool { return a.String() == b.String() }

func TestParseConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := parseConfig([]byte("public_url: http://x\n"), env(nil))
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if got := cfg.Location.String(); got != "Europe/Amsterdam" {
		t.Errorf("got location %s, want Europe/Amsterdam", got)
	}
	want := device.Schedule{First: 7 * time.Hour, Battery: time.Hour, USB: 5 * time.Minute, Detail: 5 * time.Minute, Location: cfg.Location}
	if diff := cmp.Diff(want, cfg.Schedule, cmp.Comparer(sameLocation)); diff != "" {
		t.Errorf("schedule (-want +got):\n%s", diff)
	}
}

func TestParseConfigEnvTypes(t *testing.T) {
	t.Parallel()
	raw := "public_url: http://x\nrenderer:\n  bun: '${BUN}'\n  dir: ${DIR}\n  watch: ${WATCH}\n"
	testCases := []struct {
		name  string
		value string
		want  RendererConfig
	}{
		{"empty", "", RendererConfig{Bun: "", Dir: "", Watch: true}},
		{"null", "null", RendererConfig{Bun: "null", Dir: "null", Watch: true}},
		{"plain", "x", RendererConfig{Bun: "x", Dir: "x", Watch: true}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := parseConfig([]byte(raw), env(map[string]string{"BUN": testCase.value, "DIR": testCase.value, "WATCH": "true"}))
			if err != nil {
				t.Fatalf("got error %v, want nil", err)
			}
			if diff := cmp.Diff(testCase.want, cfg.Renderer); diff != "" {
				t.Errorf("renderer (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseConfigErrors(t *testing.T) {
	t.Parallel()
	device := func(fields string) string {
		return "public_url: http://x\ndevices:\n  a: {" + fields + "}\n"
	}
	testCases := []struct {
		name string
		yaml string
		want string
	}{
		{"missing env", "public_url: ${NOPE}\n", "NOPE"},
		{"unknown key", "public_url: http://x\nbogus: 1\n", "bogus"},
		{"empty", "", "public_url"},
		{"no public_url", "timezone: UTC\n", "public_url"},
		{"relative public_url", "public_url: /x\n", "absolute"},
		{"bad timezone", "public_url: http://x\ntimezone: Mars/Base\n", "timezone"},
		{"bad wake time", "public_url: http://x\nschedule: {first_wake: '7am'}\n", "HH:MM"},
		{"bad last wake", "public_url: http://x\nschedule: {last_wake: '24:00'}\n", "HH:MM"},
		{"sub-minute interval", "public_url: http://x\nschedule: {interval_battery: 30s}\n", "interval_battery"},
		{"interval over a day", "public_url: http://x\nschedule: {interval_usb: 25h}\n", "interval_usb"},
		{"bad power", device("mac: 'aa:bb:cc:dd:ee:ff', layout: l, pages: [p], power: mains"), "power must be"},
		{"zero detail timeout", "public_url: http://x\nschedule: {detail_timeout: 0s}\n", "detail_timeout"},
		{"bad mac", device("mac: nope, layout: l, pages: [p]"), "mac"},
		{"duplicate mac", "public_url: http://x\ndevices:\n  a: {mac: 'aa:bb:cc:dd:ee:ff', layout: l, pages: [p]}\n  b: {mac: 'AA-BB-CC-DD-EE-FF', layout: l, pages: [p]}\n", "already used"},
		{"bad rotation", device("mac: 'aa:bb:cc:dd:ee:ff', rotation: 45, layout: l, pages: [p]"), "rotation"},
		{"no layout", device("mac: 'aa:bb:cc:dd:ee:ff', pages: [p]"), "layout"},
		{"no pages", device("mac: 'aa:bb:cc:dd:ee:ff', layout: l"), "pages"},
		{"name with slash", "public_url: http://x\ndevices:\n  a/b: {mac: 'aa:bb:cc:dd:ee:ff', layout: l, pages: [p]}\n", "letters"},
		{"reserved name", "public_url: http://x\ndevices:\n  page: {mac: 'aa:bb:cc:dd:ee:ff', layout: l, pages: [p]}\n", "reserved"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseConfig([]byte(testCase.yaml), env(nil))
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("got %v, want error containing %q", err, testCase.want)
			}
		})
	}
}
