package device

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/xaroth/dashboard/screen"
)

type metrics struct {
	wakesTotal   *prometheus.CounterVec
	batteryVolts *prometheus.GaugeVec
	rssi         *prometheus.GaugeVec
	lastWake     *prometheus.GaugeVec
}

// newMetrics registers the device metrics with reg; nil registers nowhere.
func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		wakesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dashboard_device_wakes_total",
			Help: "Display requests by device and Update-Source.",
		}, []string{"device", "source"}),
		batteryVolts: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dashboard_device_battery_volts",
			Help: "Battery-Voltage reported on the device's latest wake.",
		}, []string{"device"}),
		rssi: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dashboard_device_rssi_dbm",
			Help: "Wi-Fi RSSI reported on the device's latest wake.",
		}, []string{"device"}),
		lastWake: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dashboard_device_last_wake_timestamp_seconds",
			Help: "Unix time of the device's latest wake.",
		}, []string{"device"}),
	}
	if reg == nil {
		return m, nil
	}
	for _, c := range []prometheus.Collector{m.wakesTotal, m.batteryVolts, m.rssi, m.lastWake} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// wakeSources are the Update-Source values kept as label values; others are
// counted as "other", so a client cannot add series at will.
var wakeSources = map[string]bool{"timer": true, "button": true, "EXT0": true}

// observe records a wake in the metrics.
func (d *Devices) observe(name string, w *screen.Wake) {
	source := w.Source
	if !wakeSources[source] {
		source = "other"
	}
	d.metrics.wakesTotal.WithLabelValues(name, source).Inc()
	d.metrics.lastWake.WithLabelValues(name).Set(float64(w.At.Unix()))
	if w.Battery != nil {
		d.metrics.batteryVolts.WithLabelValues(name).Set(*w.Battery)
	}
	if w.RSSI != nil {
		d.metrics.rssi.WithLabelValues(name).Set(float64(*w.RSSI))
	}
}
