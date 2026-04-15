// Package metrics defines all Prometheus metrics for myPresence.
// Metrics are registered on the default registry via promauto.
package metrics

import (
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ─── HTTP ────────────────────────────────────────────────────────────────────

var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mypresence_http_requests_total",
		Help: "Total number of HTTP requests handled.",
	}, []string{"method", "path", "status_class"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mypresence_http_request_duration_seconds",
		Help:    "HTTP request latency distribution.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"method", "path"})
)

// ─── Authentication ───────────────────────────────────────────────────────────

var (
	AuthLoginsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mypresence_auth_logins_total",
		Help: "Total login attempts by method and result.",
	}, []string{"method", "result"}) // method: local|saml  result: success|failure

	AuthLogoutsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mypresence_auth_logouts_total",
		Help: "Total number of logouts.",
	})
)

// ─── Presence operations ──────────────────────────────────────────────────────

var (
	// PresenceOpsTotal counts each call to set/clear (one per API call).
	PresenceOpsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mypresence_presence_operations_total",
		Help: "Total presence set/clear operations.",
	}, []string{"action", "half"}) // action: set|clear  half: full|AM|PM|all

	// PresenceDaysTotal counts individual day*user records written/deleted.
	PresenceDaysTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mypresence_presence_days_total",
		Help: "Total presence day-records written or deleted.",
	}, []string{"action"}) // action: set|clear
)

// ─── DB gauges (collected on each scrape via callback) ───────────────────────

// DBStats holds point-in-time counts fetched from the database.
type DBStats struct {
	Users          float64
	ActiveSessions float64
	Teams          float64
	Statuses       float64
	Presences      float64
	Floorplans     float64
	Seats          float64
}

// RegisterDBCollector constructs and registers a custom Prometheus collector
// that invokes fn on every scrape to obtain current DB counts.
func RegisterDBCollector(fn func() DBStats) {
	prometheus.MustRegister(newDBCollector(fn))
}

type dbCollector struct {
	fn func() DBStats

	descUsers      *prometheus.Desc
	descSessions   *prometheus.Desc
	descTeams      *prometheus.Desc
	descStatuses   *prometheus.Desc
	descPresences  *prometheus.Desc
	descFloorplans *prometheus.Desc
	descSeats      *prometheus.Desc
}

func newDBCollector(fn func() DBStats) *dbCollector {
	return &dbCollector{
		fn:             fn,
		descUsers:      prometheus.NewDesc("mypresence_db_users_total", "Total registered users.", nil, nil),
		descSessions:   prometheus.NewDesc("mypresence_db_sessions_active_total", "Currently active sessions.", nil, nil),
		descTeams:      prometheus.NewDesc("mypresence_db_teams_total", "Total teams.", nil, nil),
		descStatuses:   prometheus.NewDesc("mypresence_db_statuses_total", "Total presence statuses defined.", nil, nil),
		descPresences:  prometheus.NewDesc("mypresence_db_presences_total", "Total presence records stored.", nil, nil),
		descFloorplans: prometheus.NewDesc("mypresence_db_floorplans_total", "Total floor plans.", nil, nil),
		descSeats:      prometheus.NewDesc("mypresence_db_seats_total", "Total seats defined across all floor plans.", nil, nil),
	}
}

func (c *dbCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.descUsers
	ch <- c.descSessions
	ch <- c.descTeams
	ch <- c.descStatuses
	ch <- c.descPresences
	ch <- c.descFloorplans
	ch <- c.descSeats
}

func (c *dbCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.fn()
	ch <- prometheus.MustNewConstMetric(c.descUsers, prometheus.GaugeValue, s.Users)
	ch <- prometheus.MustNewConstMetric(c.descSessions, prometheus.GaugeValue, s.ActiveSessions)
	ch <- prometheus.MustNewConstMetric(c.descTeams, prometheus.GaugeValue, s.Teams)
	ch <- prometheus.MustNewConstMetric(c.descStatuses, prometheus.GaugeValue, s.Statuses)
	ch <- prometheus.MustNewConstMetric(c.descPresences, prometheus.GaugeValue, s.Presences)
	ch <- prometheus.MustNewConstMetric(c.descFloorplans, prometheus.GaugeValue, s.Floorplans)
	ch <- prometheus.MustNewConstMetric(c.descSeats, prometheus.GaugeValue, s.Seats)
}

// ─── HTTP instrumentation middleware ─────────────────────────────────────────

// numericSegment matches a URL path segment that is a bare integer.
var numericSegment = regexp.MustCompile(`/\d+`)

// normalizePath replaces pure-numeric path segments with {id} to avoid
// label cardinality explosion (e.g. /admin/users/42/logs → /admin/users/{id}/logs).
func normalizePath(path string) string {
	return numericSegment.ReplaceAllString(path, "/{id}")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Instrument wraps a handler and records HTTP request metrics.
// Paths starting with /static/, /floorplan-img/, /data/, or /metrics itself
// are skipped to avoid noise.
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/metrics" ||
			len(path) >= 8 && path[:8] == "/static/" ||
			len(path) >= 14 && path[:14] == "/floorplan-img" ||
			len(path) >= 6 && path[:6] == "/data/" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		normalized := normalizePath(path)
		statusClass := fmt.Sprintf("%dxx", rec.status/100)
		dur := time.Since(start).Seconds()

		HTTPRequestsTotal.WithLabelValues(r.Method, normalized, statusClass).Inc()
		HTTPRequestDuration.WithLabelValues(r.Method, normalized).Observe(dur)
	})
}
