package module

import "github.com/prometheus/client_golang/prometheus/testutil"

// UpdateCount is how many updates of module ended with result.
func UpdateCount(module, result string) float64 {
	return testutil.ToFloat64(updatesTotal.WithLabelValues(module, result))
}

// LastSuccess is the module's last success time in the metrics.
func LastSuccess(module string) float64 {
	return testutil.ToFloat64(lastSuccess.WithLabelValues(module))
}
