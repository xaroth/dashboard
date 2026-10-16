package device_test

import (
	"net/http"
	"testing"

	"github.com/xaroth/dashboard/device"
)

func TestDisplayMetrics(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		device    string
		source    string
		simulated bool
		label     string
		wakes     float64
		battery   float64
		rssi      float64
	}{
		{"device", "metrics_device", "timer", false, "timer", 1, 4.05, -61},
		{"unknown source", "metrics_unknown", "made-up", false, "other", 1, 4.05, -61},
		{"simulated", "metrics_simulated", "timer", true, "timer", 0, 0, 0},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, func(o *device.Options) {
				o.Devices[0].Name = testCase.device
				o.Simulated = testCase.simulated
			})
			if code, body := e.display(t, e.setup(t), testCase.source); code != http.StatusOK {
				t.Fatalf("display: got %d %v", code, body)
			}
			if got := device.Wakes(testCase.device, testCase.label); got != testCase.wakes {
				t.Errorf("got %v wakes, want %v", got, testCase.wakes)
			}
			if got := device.Battery(testCase.device); got != testCase.battery {
				t.Errorf("got battery %v, want %v", got, testCase.battery)
			}
			if got := device.RSSI(testCase.device); got != testCase.rssi {
				t.Errorf("got RSSI %v, want %v", got, testCase.rssi)
			}
		})
	}
}
