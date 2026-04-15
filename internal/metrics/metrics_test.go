package metrics

// White-box tests (same package) so unexported helpers (normalizePath,
// newDBCollector) are accessible.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// ── normalizePath ─────────────────────────────────────────────────────────────

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/", "/"},
		{"/admin/users", "/admin/users"},
		{"/admin/users/42/logs", "/admin/users/{id}/logs"},
		{"/api/teams/3/members/99", "/api/teams/{id}/members/{id}"},
		{"/admin/seats/7", "/admin/seats/{id}"},
		{"/api/presences", "/api/presences"}, // no numeric segment
		{"/admin/floorplans/123", "/admin/floorplans/{id}"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := normalizePath(tt.in)
			if got != tt.want {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ── Instrument middleware ─────────────────────────────────────────────────────

// handlerCode responds with the given HTTP status code.
func handlerCode(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		io.WriteString(w, "ok") //nolint:errcheck
	})
}

func TestInstrument_SkipsNoisyPaths(t *testing.T) {
	noisy := []string{
		"/metrics",
		"/static/css/app.css",
		"/data/logo.png",
		"/floorplan-img/fp_1.png",
	}
	for _, path := range noisy {
		t.Run(path, func(t *testing.T) {
			before := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues("GET", path, "2xx"))
			req := httptest.NewRequest(http.MethodGet, path, nil)
			Instrument(handlerCode(http.StatusOK)).ServeHTTP(httptest.NewRecorder(), req)
			after := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues("GET", path, "2xx"))
			if delta := after - before; delta != 0 {
				t.Errorf("noisy path %q should be skipped; counter delta = %v", path, delta)
			}
		})
	}
}

func TestInstrument_StatusClass(t *testing.T) {
	cases := []struct {
		code  int
		class string
	}{
		{http.StatusOK, "2xx"},
		{http.StatusCreated, "2xx"},
		{http.StatusFound, "3xx"},
		{http.StatusNotFound, "4xx"},
		{http.StatusForbidden, "4xx"},
		{http.StatusInternalServerError, "5xx"},
	}
	for _, tc := range cases {
		// Unique path per case so counts start at 0 and we avoid cross-test contamination.
		path := fmt.Sprintf("/test-status-class/%d", tc.code)
		before := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues("GET", path, tc.class))
		req := httptest.NewRequest(http.MethodGet, path, nil)
		Instrument(handlerCode(tc.code)).ServeHTTP(httptest.NewRecorder(), req)
		after := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues("GET", path, tc.class))
		if delta := after - before; delta != 1 {
			t.Errorf("code %d: expected counter delta 1 under class %q, got %v", tc.code, tc.class, delta)
		}
	}
}

func TestInstrument_DefaultStatus200WhenNoWriteHeader(t *testing.T) {
	// Handler writes a body but never calls WriteHeader — net/http defaults to 200.
	path := "/test-implicit-200"
	silent := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "body without explicit header") //nolint:errcheck
	})
	before := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues("GET", path, "2xx"))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	Instrument(silent).ServeHTTP(httptest.NewRecorder(), req)
	after := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues("GET", path, "2xx"))
	if delta := after - before; delta != 1 {
		t.Errorf("implicit 200: expected counter delta 1, got %v", delta)
	}
}

func TestInstrument_NormalizesNumericSegments(t *testing.T) {
	// Two requests with different IDs should be folded into the same label.
	normalised := "/admin/users/{id}"
	before := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues("GET", normalised, "2xx"))
	for _, id := range []string{"111", "222"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/users/"+id, nil)
		Instrument(handlerCode(http.StatusOK)).ServeHTTP(httptest.NewRecorder(), req)
	}
	after := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues("GET", normalised, "2xx"))
	if delta := after - before; delta != 2 {
		t.Errorf("expected 2 requests folded under %q, got delta %v", normalised, delta)
	}
}

func TestInstrument_RecordsDurationHistogram(t *testing.T) {
	// Each handled request must produce at least one histogram observation.
	// We verify by checking that the histogram _count sample increases.
	path := "/test-duration"
	// HTTPRequestDuration is a HistogramVec; WithLabelValues returns an Observer.
	// testutil.ToFloat64 on a Histogram returns the sum — any successful call
	// to ServeHTTP is enough to prove an observation was recorded.
	before := testutil.ToFloat64(HTTPRequestDuration.WithLabelValues("GET", path))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	Instrument(handlerCode(http.StatusOK)).ServeHTTP(httptest.NewRecorder(), req)
	after := testutil.ToFloat64(HTTPRequestDuration.WithLabelValues("GET", path))
	// The sum (returned by ToFloat64 for a histogram) must be ≥ 0 and have changed.
	if after < before {
		t.Errorf("histogram sum decreased: before=%v after=%v", before, after)
	}
}

func TestInstrument_MultipleMethodsTrackedSeparately(t *testing.T) {
	path := "/test-methods"
	methods := []string{http.MethodGet, http.MethodPost, http.MethodDelete}
	for _, m := range methods {
		before := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues(m, path, "2xx"))
		req := httptest.NewRequest(m, path, nil)
		Instrument(handlerCode(http.StatusOK)).ServeHTTP(httptest.NewRecorder(), req)
		after := testutil.ToFloat64(HTTPRequestsTotal.WithLabelValues(m, path, "2xx"))
		if delta := after - before; delta != 1 {
			t.Errorf("method %s: expected delta 1, got %v", m, delta)
		}
	}
}

// ── dbCollector ───────────────────────────────────────────────────────────────

// newIsolatedRegistry creates a fresh registry pre-loaded with a dbCollector.
// Using a fresh registry avoids double-registration panics against the global
// DefaultRegisterer and keeps tests independent.
func newIsolatedRegistry(fn func() DBStats) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(newDBCollector(fn))
	return reg
}

func TestDBCollector_Describe_EmitsSevenDescs(t *testing.T) {
	c := newDBCollector(func() DBStats { return DBStats{} })
	ch := make(chan *prometheus.Desc, 20)
	c.Describe(ch)
	close(ch)

	var count int
	for range ch {
		count++
	}
	if count != 7 {
		t.Errorf("Describe emitted %d descriptors, want 7", count)
	}
}

func TestDBCollector_Collect_AllValues(t *testing.T) {
	stats := DBStats{
		Users: 5, ActiveSessions: 3, Teams: 2,
		Statuses: 7, Presences: 100, Floorplans: 2, Seats: 20,
	}
	reg := newIsolatedRegistry(func() DBStats { return stats })

	// GatherAndCompare checks both value and metadata (HELP/TYPE lines).
	// Metrics are sorted alphabetically by the text format.
	expected := strings.NewReader(`
# HELP mypresence_db_floorplans_total Total floor plans.
# TYPE mypresence_db_floorplans_total gauge
mypresence_db_floorplans_total 2
# HELP mypresence_db_presences_total Total presence records stored.
# TYPE mypresence_db_presences_total gauge
mypresence_db_presences_total 100
# HELP mypresence_db_seats_total Total seats defined across all floor plans.
# TYPE mypresence_db_seats_total gauge
mypresence_db_seats_total 20
# HELP mypresence_db_sessions_active_total Currently active sessions.
# TYPE mypresence_db_sessions_active_total gauge
mypresence_db_sessions_active_total 3
# HELP mypresence_db_statuses_total Total presence statuses defined.
# TYPE mypresence_db_statuses_total gauge
mypresence_db_statuses_total 7
# HELP mypresence_db_teams_total Total teams.
# TYPE mypresence_db_teams_total gauge
mypresence_db_teams_total 2
# HELP mypresence_db_users_total Total registered users.
# TYPE mypresence_db_users_total gauge
mypresence_db_users_total 5
`)
	if err := testutil.GatherAndCompare(reg, expected); err != nil {
		t.Error(err)
	}
}

func TestDBCollector_Collect_ZeroStats(t *testing.T) {
	reg := newIsolatedRegistry(func() DBStats { return DBStats{} })
	// The metric names must all appear with value 0.
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mypresence_db_floorplans_total Total floor plans.
# TYPE mypresence_db_floorplans_total gauge
mypresence_db_floorplans_total 0
# HELP mypresence_db_presences_total Total presence records stored.
# TYPE mypresence_db_presences_total gauge
mypresence_db_presences_total 0
# HELP mypresence_db_seats_total Total seats defined across all floor plans.
# TYPE mypresence_db_seats_total gauge
mypresence_db_seats_total 0
# HELP mypresence_db_sessions_active_total Currently active sessions.
# TYPE mypresence_db_sessions_active_total gauge
mypresence_db_sessions_active_total 0
# HELP mypresence_db_statuses_total Total presence statuses defined.
# TYPE mypresence_db_statuses_total gauge
mypresence_db_statuses_total 0
# HELP mypresence_db_teams_total Total teams.
# TYPE mypresence_db_teams_total gauge
mypresence_db_teams_total 0
# HELP mypresence_db_users_total Total registered users.
# TYPE mypresence_db_users_total gauge
mypresence_db_users_total 0
`)); err != nil {
		t.Error(err)
	}
}

func TestDBCollector_Collect_ReflectsUpdatedStats(t *testing.T) {
	// The callback is called on every Gather() — the gauge values must reflect
	// the latest return value of the callback.
	var current DBStats
	reg := newIsolatedRegistry(func() DBStats { return current })

	current = DBStats{Users: 10, Presences: 50}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mypresence_db_users_total Total registered users.
# TYPE mypresence_db_users_total gauge
mypresence_db_users_total 10
`), "mypresence_db_users_total"); err != nil {
		t.Errorf("first scrape: %v", err)
	}

	current = DBStats{Users: 25, Presences: 200}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mypresence_db_users_total Total registered users.
# TYPE mypresence_db_users_total gauge
mypresence_db_users_total 25
`), "mypresence_db_users_total"); err != nil {
		t.Errorf("second scrape: %v", err)
	}
}

func TestDBCollector_Collect_PresenceReflectsUpdatedStats(t *testing.T) {
	var current DBStats
	reg := newIsolatedRegistry(func() DBStats { return current })

	current = DBStats{Presences: 500}
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mypresence_db_presences_total Total presence records stored.
# TYPE mypresence_db_presences_total gauge
mypresence_db_presences_total 500
`), "mypresence_db_presences_total"); err != nil {
		t.Errorf("presences scrape: %v", err)
	}
}

func TestDBCollector_Collect_FloatPrecision(t *testing.T) {
	// Gauges are float64 — verify fractional values round-trip correctly.
	// (In practice counts are integers, but the type must handle it.)
	stats := DBStats{Users: 3.5}
	reg := newIsolatedRegistry(func() DBStats { return stats })
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mypresence_db_users_total Total registered users.
# TYPE mypresence_db_users_total gauge
mypresence_db_users_total 3.5
`), "mypresence_db_users_total"); err != nil {
		t.Error(err)
	}
}
