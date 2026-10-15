// package main cannot be imported, so its tests are internal.
package main

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/service"
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
devices:
  hallway:
    mac: "aa-bb-cc-dd-ee-ff"
    rotation: '${ROT}'
    pages: [hello]
    params:
      hello.events: {limit: 8}
modules:
  calendar:
    ical_url: ${ICAL}
`
	cfg, err := parseConfig([]byte(raw), env(map[string]string{"ICAL": "https://x/y.ics?a=1#b: c", "ROT": "90"}))
	if err != nil {
		t.Fatalf("got error %v", err)
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
	want := service.DefaultConfig()
	want.PublicURL = "http://192.168.1.10:8080"
	want.Devices = map[string]service.Device{"hallway": {
		MAC:      "aa-bb-cc-dd-ee-ff",
		Rotation: 90,
		Pages:    []string{"hello"},
		Params:   map[string]map[string]any{"hello.events": {"limit": 8}},
	}}
	if diff := cmp.Diff(want, cfg); diff != "" {
		t.Errorf("config (-want +got):\n%s", diff)
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
		{"bad mac", device("mac: nope, pages: [p]"), "mac"},
		{"duplicate mac", "public_url: http://x\ndevices:\n  a: {mac: 'aa:bb:cc:dd:ee:ff', pages: [p]}\n  b: {mac: 'AA-BB-CC-DD-EE-FF', pages: [p]}\n", "already used"},
		{"bad rotation", device("mac: 'aa:bb:cc:dd:ee:ff', rotation: 45, pages: [p]"), "rotation"},
		{"no pages", device("mac: 'aa:bb:cc:dd:ee:ff'"), "pages"},
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
