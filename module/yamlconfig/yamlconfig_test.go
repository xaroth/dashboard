package yamlconfig_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/yamlconfig"
)

type config struct {
	Key string `yaml:"key"`
}

func TestParse(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		yaml      string
		wantCfg   config
		wantSched *module.Schedule
	}{
		{"empty", "test:", config{Key: "default"}, nil},
		{"keys", "test: {key: k}", config{Key: "k"}, nil},
		{"schedule", "test: {key: k, schedule: {update_interval: 1m}}", config{Key: "k"}, &module.Schedule{UpdateInterval: time.Minute}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			sections, err := yamlconfig.Parse([]byte(testCase.yaml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			c := sections["test"]
			cfg := config{Key: "default"}
			if c.Decode != nil {
				if err := c.Decode(&cfg); err != nil {
					t.Fatalf("Decode: %v", err)
				}
			}
			if diff := cmp.Diff(testCase.wantCfg, cfg); diff != "" {
				t.Errorf("config (-want +got):\n%s", diff)
			}
			var sched *module.Schedule
			if c.Schedule != nil {
				sched = &module.Schedule{}
				if err := c.Schedule(sched); err != nil {
					t.Fatalf("Schedule: %v", err)
				}
			}
			if diff := cmp.Diff(testCase.wantSched, sched); diff != "" {
				t.Errorf("schedule (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	if _, err := yamlconfig.Parse([]byte("test: [1]")); err == nil || !strings.Contains(err.Error(), "modules.test: must be a mapping") {
		t.Errorf("got %v, want a mapping error", err)
	}
	sections, err := yamlconfig.Parse([]byte("test: {bogus: 1}"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := sections["test"].Decode(&config{}); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("got %v, want an unknown key error", err)
	}
}
