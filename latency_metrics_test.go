package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLatencyHistogramBucketsMatchInclusiveBounds(t *testing.T) {
	var r latencyRecorder
	r.Observe("fam", "lane", 500*time.Microsecond)  // <= 1ms
	r.Observe("fam", "lane", time.Millisecond)      // inclusive 1ms bound
	r.Observe("fam", "lane", 1500*time.Microsecond) // <= 2ms
	r.Observe("fam", "lane", 10*time.Minute)        // +Inf

	snap := r.Snapshot()["fam"]["lane"]
	if len(snap.Counts) != len(latencyBucketBoundsMillis)+1 {
		t.Fatalf("counts len = %d, want %d", len(snap.Counts), len(latencyBucketBoundsMillis)+1)
	}
	if snap.Counts[0] != 2 || snap.Counts[1] != 1 || snap.Counts[len(snap.Counts)-1] != 1 {
		t.Fatalf("unexpected bucket counts %v", snap.Counts)
	}
	wantSum := uint64(500 + 1000 + 1500 + 600_000_000)
	if snap.SumMicros != wantSum {
		t.Fatalf("sum_micros = %d, want %d", snap.SumMicros, wantSum)
	}
}

func TestTipEventLogRecordsLooksUpAndMarksNotified(t *testing.T) {
	var log tipEventLog
	hash := [32]byte{0xaa}
	received := time.UnixMicro(1_000_000)
	accepted := time.UnixMicro(1_250_000)
	log.record(tipEvent{Hash: hash, Source: tipSourceGossip, ReceivedAt: received, AcceptedAt: accepted})

	notified := time.UnixMicro(1_300_000)
	log.markNotified(hash, notified)
	got, ok := log.lookup(hash)
	if !ok {
		t.Fatal("expected tip event")
	}
	if got.Source != tipSourceGossip || !got.ReceivedAt.Equal(received) || !got.AcceptedAt.Equal(accepted) || !got.NotifiedAt.Equal(notified) {
		t.Fatalf("unexpected tip event %+v", got)
	}

	// Older entries age out once the ring wraps.
	for i := 0; i < tipEventHistory; i++ {
		log.record(tipEvent{Hash: [32]byte{byte(i + 1), 0x01}, AcceptedAt: accepted})
	}
	if _, ok := log.lookup(hash); ok {
		t.Fatal("expected oldest tip event to be evicted")
	}
}

func TestHandleSubmitBlockRecordsRejectedLatency(t *testing.T) {
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()
	mustAddGenesisBlock(t, chain)

	d := &Daemon{chain: chain}
	s := NewAPIServer(d, nil, nil, t.TempDir(), nil)

	var wrongPrev [32]byte
	wrongPrev[0] = 1
	body, err := json.Marshal(&Block{Header: BlockHeader{Version: 1, Height: chain.Height() + 1, PrevHash: wrongPrev}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rr := httptest.NewRecorder()
	s.handleSubmitBlock(rr, httptest.NewRequest(http.MethodPost, "/api/mining/submitblock", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}

	perf := httptest.NewRecorder()
	s.handlePerfLatency(perf, httptest.NewRequest(http.MethodGet, "/api/perf/latency", nil))
	var resp struct {
		Families map[string]map[string]latencyHistogramSnapshot `json:"families"`
	}
	if err := json.Unmarshal(perf.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode perf response: %v body=%q", err, perf.Body.String())
	}
	lanes := resp.Families[latencyFamilySubmitBlock]
	for _, lane := range []string{"request_decode", "rejected_total"} {
		if count := sumCounts(lanes[lane].Counts); count != 1 {
			t.Fatalf("lane %s count = %d, want 1 (lanes=%v)", lane, count, lanes)
		}
	}
	if _, ok := lanes["total"]; ok {
		t.Fatal("rejected submit must not record the accepted total lane")
	}
}

func TestHandleEventsNewBlockCarriesAcceptanceTimes(t *testing.T) {
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()
	mustAddGenesisBlock(t, chain)

	daemon, stopDaemon := mustStartTestDaemon(t, chain)
	defer stopDaemon()
	server := httptest.NewServer(http.HandlerFunc((&APIServer{daemon: daemon}).handleEvents))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	readEvent := func(name string) map[string]any {
		t.Helper()
		var event string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("read SSE: %v", err)
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") && event == name {
				var payload map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
					t.Fatalf("decode %s payload: %v", name, err)
				}
				return payload
			}
		}
	}
	readEvent("connected")

	block := chain.GetBlockByHeight(0)
	received := time.Now().Add(-30 * time.Millisecond)
	accepted := received.Add(20 * time.Millisecond)
	daemon.recordTipAccepted(block.Hash(), tipSourceGossip, received, accepted)
	daemon.notifyBlock(block)

	payload := readEvent("new_block")
	if payload["source"] != tipSourceGossip {
		t.Fatalf("source = %v, want gossip (payload=%v)", payload["source"], payload)
	}
	if got := int64(payload["received_at_unix_us"].(float64)); got != received.UnixMicro() {
		t.Fatalf("received_at_unix_us = %d, want %d", got, received.UnixMicro())
	}
	if got := int64(payload["accepted_at_unix_us"].(float64)); got != accepted.UnixMicro() {
		t.Fatalf("accepted_at_unix_us = %d, want %d", got, accepted.UnixMicro())
	}

	deadline := time.Now().Add(time.Second)
	for sumCounts(daemon.LatencySnapshot()[latencyFamilyEvents]["new_block_write"].Counts) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("expected SSE delivery latency to be recorded")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func sumCounts(counts []uint64) uint64 {
	var total uint64
	for _, c := range counts {
		total += c
	}
	return total
}
