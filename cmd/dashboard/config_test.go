package main

import (
	"strings"
	"testing"
	"time"
)

const validConfig = `
public_url: http://192.168.1.10:8080
devices:
  hallway:
    mac: "aa-bb-cc-dd-ee-ff"
    rotation: 90
    pages: [hello]
modules:
  calendar:
    ical_url: ${ICAL}
`

func env(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig([]byte(validConfig), env(map[string]string{"ICAL": "https://x/y.ics?a=1#b: c"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Devices["hallway"].Rotation; got != 90 {
		t.Errorf("rotation = %d, want 90", got)
	}
	if got := cfg.Schedule.DetailTimeout; got != 5*time.Minute {
		t.Errorf("default detail_timeout = %v", got)
	}
	cal := cfg.Modules["calendar"]
	var m map[string]string
	if err := cal.Decode(&m); err != nil {
		t.Fatal(err)
	}
	if want := "https://x/y.ics?a=1#b: c"; m["ical_url"] != want {
		t.Errorf("ical_url = %q, want %q", m["ical_url"], want)
	}
}

func TestParseConfigErrors(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{"missing env", validConfig, "ICAL"},
		{"unknown key", "public_url: http://x\nbogus: 1\n", "bogus"},
		{"no public_url", "timezone: UTC\n", "public_url"},
		{"relative public_url", "public_url: /x\n", "absolute"},
		{"bad timezone", "public_url: http://x\ntimezone: Mars/Base\n", "timezone"},
		{"bad mac", "public_url: http://x\ndevices:\n  a: {mac: nope, pages: [p]}\n", "mac"},
		{"dup mac", "public_url: http://x\ndevices:\n  a: {mac: 'aa:bb:cc:dd:ee:ff', pages: [p]}\n  b: {mac: 'AA-BB-CC-DD-EE-FF', pages: [p]}\n", "already used"},
		{"bad rotation", "public_url: http://x\ndevices:\n  a: {mac: 'aa:bb:cc:dd:ee:ff', rotation: 45, pages: [p]}\n", "rotation"},
		{"no pages", "public_url: http://x\ndevices:\n  a: {mac: 'aa:bb:cc:dd:ee:ff'}\n", "pages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConfig([]byte(tt.yaml), env(nil))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestParseConfigEnvTypes(t *testing.T) {
	y := "public_url: http://x\ndevices:\n  a: {mac: 'aa:bb:cc:dd:ee:ff', rotation: '${ROT}', pages: [p]}\n"
	cfg, err := parseConfig([]byte(y), env(map[string]string{"ROT": "180"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Devices["a"].Rotation; got != 180 {
		t.Errorf("rotation = %d, want 180", got)
	}
}
