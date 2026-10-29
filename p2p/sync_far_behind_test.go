package p2p

import (
	"sync"
	"testing"
)

func newFarBehindTestManager(tipHeight uint64) *SyncManager {
	return &SyncManager{
		mu:        sync.RWMutex{},
		getStatus: func() ChainStatus { return ChainStatus{Height: tipHeight} },
	}
}

func TestIsSyncingFarBehindNotSyncing(t *testing.T) {
	sm := newFarBehindTestManager(100)
	if sm.IsSyncingFarBehind(2) {
		t.Fatal("not syncing should never report far behind")
	}
}

func TestIsSyncingFarBehindNearTip(t *testing.T) {
	sm := newFarBehindTestManager(100)
	sm.syncing = true
	sm.syncTarget = 101

	if sm.IsSyncingFarBehind(2) {
		t.Fatal("one-block catch-up should stay within tolerance")
	}
}

func TestIsSyncingFarBehindAtTargetDuringMempoolPhase(t *testing.T) {
	sm := newFarBehindTestManager(101)
	sm.syncing = true
	sm.syncTarget = 101

	if sm.IsSyncingFarBehind(0) {
		t.Fatal("tip at target should not report far behind even with zero tolerance")
	}
}

func TestIsSyncingFarBehindDeepSync(t *testing.T) {
	sm := newFarBehindTestManager(100)
	sm.syncing = true
	sm.syncTarget = 500

	if !sm.IsSyncingFarBehind(2) {
		t.Fatal("deep sync must report far behind")
	}
}

func TestIsSyncingFarBehindExactTolerance(t *testing.T) {
	sm := newFarBehindTestManager(100)
	sm.syncing = true
	sm.syncTarget = 102

	if sm.IsSyncingFarBehind(2) {
		t.Fatal("gap equal to tolerance should still serve templates")
	}
	if !sm.IsSyncingFarBehind(1) {
		t.Fatal("gap above tolerance must report far behind")
	}
}

func TestBlocksBehindCountsOnlyAGapToTheTarget(t *testing.T) {
	sm := newFarBehindTestManager(100)
	if got := sm.BlocksBehind(); got != 0 {
		t.Fatalf("no sync run: %d blocks behind, want 0", got)
	}
	sm.syncing = true
	sm.syncTarget = 100
	if got := sm.BlocksBehind(); got != 0 {
		t.Fatalf("overlap re-download at the tip: %d blocks behind, want 0", got)
	}
	sm.syncTarget = 103
	if got := sm.BlocksBehind(); got != 3 {
		t.Fatalf("target 3 ahead: %d blocks behind, want 3", got)
	}
	sm.syncTarget = 90
	if got := sm.BlocksBehind(); got != 0 {
		t.Fatalf("target below the tip: %d blocks behind, want 0", got)
	}
}
