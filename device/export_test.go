package device

import "github.com/prometheus/client_golang/prometheus/testutil"

// Wakes is the device's wake count for source in the metrics.
func Wakes(device, source string) float64 {
	return testutil.ToFloat64(wakesTotal.WithLabelValues(device, source))
}

// Battery is the device's battery voltage in the metrics.
func Battery(device string) float64 {
	return testutil.ToFloat64(batteryVolts.WithLabelValues(device))
}

// RSSI is the device's RSSI in the metrics.
func RSSI(device string) float64 {
	return testutil.ToFloat64(rssi.WithLabelValues(device))
}
