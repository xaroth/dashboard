package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/felixge/httpsnoop"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "dashboard_http_request_duration_seconds",
	Help:    "Duration of HTTP requests by listener, route pattern and status code.",
	Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
}, []string{"listener", "route", "code"})

// untraced are probe and scrape paths, polled often enough to bury the traces worth reading.
var untraced = map[string]bool{"/healthz": true, "/metrics": true}

// instrument traces each request and records its duration under the
// listener's name and the route pattern it matched.
func instrument(listener string, h http.Handler) http.Handler {
	measured := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := httpsnoop.CaptureMetrics(h, w, r)
		// ServeMux sets the pattern on the request it was given.
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		requestDuration.WithLabelValues(listener, route, strconv.Itoa(m.Code)).Observe(m.Duration.Seconds())
	})
	return otelhttp.NewHandler(measured, listener,
		otelhttp.WithFilter(func(r *http.Request) bool { return !untraced[r.URL.Path] }))
}

// setupTracing installs the global tracer provider and propagator. Spans are
// exported over OTLP/HTTP when an OTLP endpoint is set in the environment
// (OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT); without
// one they are dropped. The returned func flushes pending spans.
func setupTracing(ctx context.Context) (shutdown func(context.Context) error, err error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES override the name.
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", "dashboard")),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
