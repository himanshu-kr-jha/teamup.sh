package system

import (
	"math"
	"sort"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// What the app itself has counted (internal/observability), read in-process
// from the same registry /metrics serves, so nothing extra is counted or kept.

const (
	requestsMetric = "teamup_http_requests_total"
	durationMetric = "teamup_http_request_duration_seconds"
	socketsMetric  = "teamup_ws_connections_active"
)

// appCounters is one reading of the cumulative counters.
type appCounters struct {
	requests uint64
	errors   uint64 // answered 5xx
	// The request-time histogram summed over every route: count, and the
	// cumulative count at each upper bound in seconds.
	count   uint64
	buckets map[float64]uint64
	sockets float64
}

func readCounters(g prometheus.Gatherer) (appCounters, error) {
	families, err := g.Gather()
	if err != nil {
		return appCounters{}, err
	}
	out := appCounters{buckets: map[float64]uint64{}}
	for _, f := range families {
		switch f.GetName() {
		case requestsMetric:
			for _, m := range f.GetMetric() {
				n := uint64(m.GetCounter().GetValue())
				out.requests += n
				for _, l := range m.GetLabel() {
					if l.GetName() == "status" {
						if code, err := strconv.Atoi(l.GetValue()); err == nil && code >= 500 {
							out.errors += n
						}
					}
				}
			}
		case durationMetric:
			for _, m := range f.GetMetric() {
				h := m.GetHistogram()
				out.count += h.GetSampleCount()
				for _, b := range h.GetBucket() {
					out.buckets[b.GetUpperBound()] += b.GetCumulativeCount()
				}
			}
		case socketsMetric:
			for _, m := range f.GetMetric() {
				out.sockets += m.GetGauge().GetValue()
			}
		}
	}
	return out, nil
}

// appActivity is what happened between two readings.
type appActivity struct {
	Requests uint64 `json:"requests"`
	Errors   uint64 `json:"errors"`
	// Typical and slow answers, in milliseconds: half of the requests took no
	// longer than P50, and 95 in a hundred no longer than P95. Nil with no
	// requests to measure.
	P50ms *float64 `json:"p50ms"`
	P95ms *float64 `json:"p95ms"`
}

// activityBetween subtracts two readings. A counter that went down means the
// app restarted in between; the later reading is then everything there is.
func activityBetween(a, b appCounters) appActivity {
	if b.requests < a.requests || b.count < a.count {
		a = appCounters{buckets: map[float64]uint64{}}
	}
	act := appActivity{Requests: b.requests - a.requests, Errors: sub(b.errors, a.errors)}
	n := b.count - a.count
	if n == 0 {
		return act
	}
	bounds := make([]float64, 0, len(b.buckets))
	for ub := range b.buckets {
		bounds = append(bounds, ub)
	}
	sort.Float64s(bounds)
	act.P50ms = quantileMs(bounds, a, b, n, 0.50)
	act.P95ms = quantileMs(bounds, a, b, n, 0.95)
	return act
}

func sub(b, a uint64) uint64 {
	if b < a {
		return 0
	}
	return b - a
}

// quantileMs finds the bucket the q-th request falls in and interpolates
// within it, as Prometheus's histogram_quantile does. Past the last finite
// bound it answers that bound: the histogram knows no more than "slower".
func quantileMs(bounds []float64, a, b appCounters, n uint64, q float64) *float64 {
	rank := q * float64(n)
	prevBound, prevCount := 0.0, 0.0
	for _, ub := range bounds {
		c := float64(sub(b.buckets[ub], a.buckets[ub]))
		if math.IsInf(ub, 1) {
			break
		}
		if c >= rank {
			v := prevBound
			if c > prevCount {
				v += (ub - prevBound) * (rank - prevCount) / (c - prevCount)
			}
			ms := math.Round(v*1000*10) / 10
			return &ms
		}
		prevBound, prevCount = ub, c
	}
	ms := math.Round(prevBound * 1000)
	return &ms
}
