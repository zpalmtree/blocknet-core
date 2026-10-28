package main

import (
	"sort"
	"sync"
	"time"
)

// latencyBucketBoundsMillis are the inclusive upper bounds of the cumulative
// latency histogram buckets. They mirror blocknet-pool's
// LATENCY_BUCKET_BOUNDS_MILLIS so pool-monitor can render daemon histograms
// next to its own with identical Prometheus buckets.
var latencyBucketBoundsMillis = [...]uint64{
	1, 2, 5, 10, 25, 50, 100, 250, 500, 750, 1_000, 1_250, 1_500, 1_750, 2_000, 2_250, 2_500,
	2_750, 3_000, 3_500, 4_000, 5_000, 7_500, 10_000, 15_000, 30_000, 60_000, 120_000, 300_000,
}

// Latency families exported by GET /api/perf/latency. Lanes within a family
// are pipeline stages, so per-stage quantiles can be compared directly.
const (
	latencyFamilySubmitBlock   = "daemon_submit_block"
	latencyFamilyPeerBlock     = "daemon_peer_block"
	latencyFamilyProcessBlock  = "daemon_process_block"
	latencyFamilyBlockTemplate = "daemon_block_template"
	latencyFamilyEvents        = "daemon_events"
)

// latencyHistogramSnapshot matches blocknet-pool's LatencyHistogramSnapshot
// JSON shape: per-bucket (not cumulative) counts with a trailing +Inf bucket.
type latencyHistogramSnapshot struct {
	BoundsMillis []uint64 `json:"bounds_millis"`
	Counts       []uint64 `json:"counts"`
	SumMicros    uint64   `json:"sum_micros"`
}

type latencyHistogram struct {
	counts    [len(latencyBucketBoundsMillis) + 1]uint64
	sumMicros uint64
}

func (h *latencyHistogram) observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	micros := uint64(d / time.Microsecond)
	idx := sort.Search(len(latencyBucketBoundsMillis), func(i int) bool {
		return latencyBucketBoundsMillis[i]*1_000 >= micros
	})
	h.counts[idx]++
	h.sumMicros += micros
}

func (h *latencyHistogram) snapshot() latencyHistogramSnapshot {
	return latencyHistogramSnapshot{
		BoundsMillis: append([]uint64(nil), latencyBucketBoundsMillis[:]...),
		Counts:       append([]uint64(nil), h.counts[:]...),
		SumMicros:    h.sumMicros,
	}
}

// latencyRecorder holds process-lifetime histograms keyed by family, then
// lane. Counts only grow, so Prometheus can compute quantiles over any range.
// The zero value is ready to use.
type latencyRecorder struct {
	mu       sync.Mutex
	families map[string]map[string]*latencyHistogram
}

func (r *latencyRecorder) Observe(family, lane string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.families == nil {
		r.families = make(map[string]map[string]*latencyHistogram)
	}
	lanes := r.families[family]
	if lanes == nil {
		lanes = make(map[string]*latencyHistogram)
		r.families[family] = lanes
	}
	h := lanes[lane]
	if h == nil {
		h = &latencyHistogram{}
		lanes[lane] = h
	}
	h.observe(d)
}

func (r *latencyRecorder) Snapshot() map[string]map[string]latencyHistogramSnapshot {
	out := make(map[string]map[string]latencyHistogramSnapshot)
	r.mu.Lock()
	defer r.mu.Unlock()
	for family, lanes := range r.families {
		snapshots := make(map[string]latencyHistogramSnapshot, len(lanes))
		for lane, h := range lanes {
			snapshots[lane] = h.snapshot()
		}
		out[family] = snapshots
	}
	return out
}

// observeProcessBlock records ProcessBlock stage timings under a source
// prefix (submit, gossip, sync) so catch-up sync never hides tip latency.
func (r *latencyRecorder) observeProcessBlock(source string, t processBlockTimings) {
	r.Observe(latencyFamilyProcessBlock, source+"_validate", t.Validate)
	if !t.Detail.SkipPoW {
		r.Observe(latencyFamilyProcessBlock, source+"_pow", t.Detail.PoW)
	}
	r.Observe(latencyFamilyProcessBlock, source+"_tx_validation", t.Detail.TxValidation)
	if t.Reorg > 0 {
		r.Observe(latencyFamilyProcessBlock, source+"_reorg", t.Reorg)
	}
	if t.Commit > 0 {
		r.Observe(latencyFamilyProcessBlock, source+"_commit", t.Commit)
	}
	r.Observe(latencyFamilyProcessBlock, source+"_total", t.Total)
}

// Tip event sources carried on the new_block SSE event.
const (
	tipSourceSubmit = "submit"
	tipSourceGossip = "gossip"
	tipSourceSync   = "sync"
)

// tipEvent records when this daemon first saw and then accepted a main-chain
// block, so /api/events consumers can measure delivery latency end to end.
type tipEvent struct {
	Hash       [32]byte
	Source     string
	ReceivedAt time.Time
	AcceptedAt time.Time
	NotifiedAt time.Time
}

const tipEventHistory = 32

// tipEventLog is a small ring of recent tip events, looked up by hash when an
// SSE subscriber writes the matching new_block event.
type tipEventLog struct {
	mu     sync.Mutex
	events [tipEventHistory]tipEvent
	next   int
}

func (l *tipEventLog) record(ev tipEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.events {
		if l.events[i].Hash == ev.Hash && !l.events[i].AcceptedAt.IsZero() {
			l.events[i] = ev
			return
		}
	}
	l.events[l.next] = ev
	l.next = (l.next + 1) % len(l.events)
}

// markNotified stamps the subscriber fan-out time, creating an entry for
// blocks whose acceptance path did not record one.
func (l *tipEventLog) markNotified(hash [32]byte, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.events {
		if l.events[i].Hash == hash && !l.events[i].AcceptedAt.IsZero() {
			l.events[i].NotifiedAt = at
			return
		}
	}
	l.events[l.next] = tipEvent{Hash: hash, AcceptedAt: at, NotifiedAt: at}
	l.next = (l.next + 1) % len(l.events)
}

func (l *tipEventLog) lookup(hash [32]byte) (tipEvent, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.events {
		if l.events[i].Hash == hash && !l.events[i].AcceptedAt.IsZero() {
			return l.events[i], true
		}
	}
	return tipEvent{}, false
}

// submitBlockTimings breaks down Daemon.SubmitBlock for a locally mined or
// pool-submitted block, in pipeline order.
type submitBlockTimings struct {
	LockWait       time.Duration
	StaleCheck     time.Duration
	Process        processBlockTimings
	MempoolUpdate  time.Duration
	CheckpointSave time.Duration
	Marshal        time.Duration
	Broadcast      time.Duration
	Notify         time.Duration
	Total          time.Duration
}

func (t submitBlockTimings) responseMicros() map[string]int64 {
	us := func(d time.Duration) int64 { return int64(d / time.Microsecond) }
	return map[string]int64{
		"lock_wait":       us(t.LockWait),
		"stale_check":     us(t.StaleCheck),
		"process":         us(t.Process.Total),
		"validate":        us(t.Process.Validate),
		"pow":             us(t.Process.Detail.PoW),
		"tx_validation":   us(t.Process.Detail.TxValidation),
		"reorg":           us(t.Process.Reorg),
		"mempool_update":  us(t.MempoolUpdate),
		"checkpoint_save": us(t.CheckpointSave),
		"marshal":         us(t.Marshal),
		"broadcast":       us(t.Broadcast),
		"notify":          us(t.Notify),
		"total":           us(t.Total),
	}
}
