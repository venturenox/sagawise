// Package otel wires the OpenTelemetry SDK: a trace provider (no exporter
// yet; add a trace.WithBatcher to ship spans) and a meter provider whose
// only reader is a Prometheus exporter. Everything instrumented through the
// global providers (otelhttp, redisotel, the engine's own metrics in
// instance_engine/metrics.go) therefore shows up on the /metrics endpoint
// main serves on SAGAWISE_METRICS_ADDR. (phase 9)
package otel

import (
	"context"
	"errors"
	"net/http"
	"os"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
)

// serviceName is the OpenTelemetry service.name unless OTEL_SERVICE_NAME
// overrides it; it appears in target_info.
const serviceName = "sagawise"

// SDK is what Setup returns: the metrics handler to serve and the shutdown
// that flushes the providers.
type SDK struct {
	// Metrics serves the Prometheus text exposition of every registered
	// metric: the engine's, otelhttp's, redisotel's, and the Go runtime's.
	Metrics http.Handler

	shutdownFuncs []func(context.Context) error
}

// Shutdown flushes and stops every provider. Each is called once; errors
// are joined.
func (s *SDK) Shutdown(ctx context.Context) error {
	var err error
	for _, fn := range s.shutdownFuncs {
		err = errors.Join(err, fn(ctx))
	}
	s.shutdownFuncs = nil
	return err
}

// Setup installs the propagator, the trace provider and the meter provider
// as OpenTelemetry's globals. Call it before anything creates instruments,
// so they bind to the real provider rather than the delegating global.
func Setup(ctx context.Context) (*SDK, error) {
	s := &SDK{}
	fail := func(err error) (*SDK, error) {
		return nil, errors.Join(err, s.Shutdown(ctx))
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// resource.Default reads OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES
	// but falls back to "unknown_service:<binary>"; the explicit name
	// replaces that fallback and yields to OTEL_SERVICE_NAME when set.
	res := resource.Default()
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		var err error
		res, err = resource.Merge(res, resource.NewSchemaless(attribute.String("service.name", serviceName)))
		if err != nil {
			return fail(err)
		}
	}

	tracerProvider := trace.NewTracerProvider(trace.WithResource(res))
	s.shutdownFuncs = append(s.shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	// One private registry: the process and Go runtime collectors give
	// process_* and go_* series; the OTel exporter adds every instrument
	// recorded through the meter provider.
	reg := promclient.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	// Scope labels (otel_scope_name and friends) are dropped: they triple
	// the label set of every series and the names are already prefixed.
	exporter, err := prometheus.New(prometheus.WithRegisterer(reg), prometheus.WithoutScopeInfo())
	if err != nil {
		return fail(err)
	}
	meterProvider := metric.NewMeterProvider(metric.WithReader(exporter), metric.WithResource(res))
	s.shutdownFuncs = append(s.shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	s.Metrics = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	return s, nil
}
