package main

import (
	"testing"
	"time"
)

func seedSizedEntries(m *Mempool, entries ...[2]int) {
	for i, e := range entries {
		var id [32]byte
		id[0], id[1] = byte(i), byte(i>>8)
		m.txByID[id] = &MempoolEntry{Tx: &Transaction{}, TxID: id, FeeRate: uint64(e[0]), Size: e[1], AddedAt: time.Unix(int64(1000+i), 0), index: -1}
		m.totalSize += e[1]
	}
}

// The estimate is the rate that ranks a new transaction inside the given
// number of blocks, as templates rank by fee rate.
func TestMempoolFeeRateFor(t *testing.T) {
	m := NewMempool(DefaultMempoolConfig(), nil, nil)
	// Room to spare: the floor.
	if got := m.FeeRateFor(1, 1000, 10, 100, 10); got != 10 {
		t.Fatalf("empty mempool: %d", got)
	}
	// 1000-byte blocks: 600 bytes at 50, then 300 at 20, then 500 at 10.
	seedSizedEntries(m, [2]int{50, 300}, [2]int{50, 300}, [2]int{20, 300}, [2]int{10, 500})
	if got := m.FeeRateFor(1, 1000, 10, 100, 10); got != 11 {
		t.Fatalf("next block, 100 bytes fit after the 20s: %d", got)
	}
	if got := m.FeeRateFor(1, 1000, 10, 200, 10); got != 21 {
		t.Fatalf("next block, 200 bytes only fit ahead of the 20s: %d", got)
	}
	if got := m.FeeRateFor(2, 1000, 10, 100, 10); got != 10 {
		t.Fatalf("two blocks hold everything: %d", got)
	}
	// The count limit counts too: two transactions a block.
	if got := m.FeeRateFor(1, 1000, 2, 100, 10); got != 51 {
		t.Fatalf("next block with two slots: %d", got)
	}
	// Transactions below the floor never rank ahead.
	m2 := NewMempool(DefaultMempoolConfig(), nil, nil)
	seedSizedEntries(m2, [2]int{5, 5000})
	if got := m2.FeeRateFor(1, 1000, 10, 100, 10); got != 10 {
		t.Fatalf("below-floor backlog: %d", got)
	}
}

// A full mempool makes room by dropping the cheapest transaction (oldest of
// equals), and only for one that pays more.
func TestMempoolEvictsLowestFeeRate(t *testing.T) {
	m := NewMempool(DefaultMempoolConfig(), nil, nil)
	seedSizedEntries(m, [2]int{30, 100}, [2]int{5, 100}, [2]int{5, 100}, [2]int{40, 100})
	if m.evictLowest(5) {
		t.Fatal("evicted for a newcomer paying no more")
	}
	if !m.evictLowest(6) {
		t.Fatal("did not evict for a higher rate")
	}
	var oldest5, newer5 [32]byte
	oldest5[0], newer5[0] = 1, 2
	if _, ok := m.txByID[oldest5]; ok {
		t.Fatal("kept the oldest of the cheapest")
	}
	if _, ok := m.txByID[newer5]; !ok {
		t.Fatal("dropped the newer cheapest one first")
	}
	if m.totalSize != 300 {
		t.Fatalf("size after eviction: %d", m.totalSize)
	}
}
