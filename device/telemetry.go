package device

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/xaroth/dashboard/render"
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

// observe records a wake in the metrics.
func (d *Devices) observe(name string, w *render.Wake) {
	if d.simulated {
		return
	}
	wakesTotal.WithLabelValues(name, w.Source).Inc()
	lastWake.WithLabelValues(name).Set(float64(w.At.Unix()))
	if w.Battery != nil {
		batteryVolts.WithLabelValues(name).Set(*w.Battery)
	}
	if w.RSSI != nil {
		rssi.WithLabelValues(name).Set(float64(*w.RSSI))
	}
}
