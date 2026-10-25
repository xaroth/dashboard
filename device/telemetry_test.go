package device_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/xaroth/dashboard/device"
)

func TestDisplayMetrics(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		source string
		label  string
	}{
		{"known source", "timer", "timer"},
		{"unknown source", "made-up", "other"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			reg := prometheus.NewRegistry()
			e := newEnv(t, func(o *device.Options) { o.Metrics = reg })
			if code, body := e.display(t, e.setup(t), testCase.source); code != http.StatusOK {
				t.Fatalf("display: got %d %v", code, body)
			}
			want := `
# HELP dashboard_device_battery_volts Battery-Voltage reported on the device's latest wake.
# TYPE dashboard_device_battery_volts gauge
dashboard_device_battery_volts{device="hallway"} 4.05
# HELP dashboard_device_last_wake_timestamp_seconds Unix time of the device's latest wake.
# TYPE dashboard_device_last_wake_timestamp_seconds gauge
dashboard_device_last_wake_timestamp_seconds{device="hallway"} 1.7920872e+09
# HELP dashboard_device_rssi_dbm Wi-Fi RSSI reported on the device's latest wake.
# TYPE dashboard_device_rssi_dbm gauge
dashboard_device_rssi_dbm{device="hallway"} -61
# HELP dashboard_device_wakes_total Display requests by device and Update-Source.
# TYPE dashboard_device_wakes_total counter
dashboard_device_wakes_total{device="hallway",source="` + testCase.label + `"} 1
`
			if err := testutil.GatherAndCompare(reg, strings.NewReader(want)); err != nil {
				t.Error(err)
			}
		})
	}
}
