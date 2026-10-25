package device

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/xaroth/dashboard/screen"
)

var (
	wakesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dashboard_device_wakes_total",
		Help: "Display requests by device and Update-Source.",
	}, []string{"device", "source"})
	batteryVolts = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dashboard_device_battery_volts",
		Help: "Battery-Voltage reported on the device's latest wake.",
	}, []string{"device"})
	rssi = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dashboard_device_rssi_dbm",
		Help: "Wi-Fi RSSI reported on the device's latest wake.",
	}, []string{"device"})
	lastWake = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dashboard_device_last_wake_timestamp_seconds",
		Help: "Unix time of the device's latest wake.",
	}, []string{"device"})
)

// wakeSources are the Update-Source values kept as label values; others are
// counted as "other", so a client cannot add series at will.
var wakeSources = map[string]bool{"timer": true, "button": true, "EXT0": true}

// observe records a wake in the metrics.
func (d *Devices) observe(name string, w *screen.Wake) {
	if d.simulated {
		return
	}
	source := w.Source
	if !wakeSources[source] {
		source = "other"
	}
	wakesTotal.WithLabelValues(name, source).Inc()
	lastWake.WithLabelValues(name).Set(float64(w.At.Unix()))
	if w.Battery != nil {
		batteryVolts.WithLabelValues(name).Set(*w.Battery)
	}
	if w.RSSI != nil {
		rssi.WithLabelValues(name).Set(float64(*w.RSSI))
	}
}
