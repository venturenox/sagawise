package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Setup serves what the global meter provider records, under the service
// name sagawise, alongside the Go runtime series.
func TestSetupServesMetrics(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	sdk, err := Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdk.Shutdown(context.Background()) })

	c, err := otel.Meter("test").Int64Counter("sagawise.test.hits")
	if err != nil {
		t.Fatal(err)
	}
	c.Add(context.Background(), 3, metric.WithAttributes())

	w := httptest.NewRecorder()
	sdk.Metrics.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := w.Body.String()
	for _, want := range []string{"sagawise_test_hits_total 3", `service_name="sagawise"`, "go_goroutines"} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "otel_scope_name") {
		t.Error("scope labels should be dropped")
	}
}
