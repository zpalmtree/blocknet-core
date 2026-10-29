package main

import (
	"testing"
	"time"
)

func TestTemplateParamsUsesCommittedTipSnapshot(t *testing.T) {
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()

	mustAddGenesisBlock(t, chain)

	snapshot := chain.TipSnapshot()
	bestHash := chain.BestHash()
	if snapshot.Height != chain.Height() {
		t.Fatalf("snapshot height = %d, want %d", snapshot.Height, chain.Height())
	}
	if snapshot.BestHash != bestHash {
		t.Fatalf("snapshot hash = %x, want %x", snapshot.BestHash[:8], bestHash[:8])
	}
	if snapshot.TotalWork != chain.TotalWork() {
		t.Fatalf("snapshot total work = %d, want %d", snapshot.TotalWork, chain.TotalWork())
	}

	params := chain.TemplateParams()
	if params.Height != chain.Height()+1 {
		t.Fatalf("template height = %d, want %d", params.Height, chain.Height()+1)
	}
	if params.PrevHash != bestHash {
		t.Fatalf("template prev hash = %x, want %x", params.PrevHash[:8], bestHash[:8])
	}
	if params.Difficulty != chain.NextDifficulty() {
		t.Fatalf("template difficulty = %d, want %d", params.Difficulty, chain.NextDifficulty())
	}
}

func TestTemplateParamsDoesNotBlockOnChainWriteLock(t *testing.T) {
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()

	mustAddGenesisBlock(t, chain)

	chain.mu.Lock()
	defer chain.mu.Unlock()
	done := make(chan BlockTemplateParams, 1)
	go func() {
		done <- chain.TemplateParams()
	}()

	select {
	case params := <-done:
		if params.Height != 1 {
			t.Fatalf("template height = %d, want 1", params.Height)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("TemplateParams blocked on chain write lock")
	}
}

func TestTipSnapshotInitializesFromReloadedStorage(t *testing.T) {
	dataDir := t.TempDir()
	chain, err := NewChain(dataDir)
	if err != nil {
		t.Fatalf("failed to create chain: %v", err)
	}

	mustAddGenesisBlock(t, chain)
	wantHash := chain.BestHash()
	wantWork := chain.TotalWork()
	if err := chain.Close(); err != nil {
		t.Fatalf("failed to close chain: %v", err)
	}

	reloaded, err := NewChain(dataDir)
	if err != nil {
		t.Fatalf("failed to reload chain: %v", err)
	}
	defer func() {
		if err := reloaded.Close(); err != nil {
			t.Fatalf("failed to close reloaded chain: %v", err)
		}
	}()

	snapshot := reloaded.TipSnapshot()
	if snapshot.Height != 0 {
		t.Fatalf("snapshot height = %d, want 0", snapshot.Height)
	}
	if snapshot.BestHash != wantHash {
		t.Fatalf("snapshot hash = %x, want %x", snapshot.BestHash[:8], wantHash[:8])
	}
	if snapshot.TotalWork != wantWork {
		t.Fatalf("snapshot total work = %d, want %d", snapshot.TotalWork, wantWork)
	}

	params := reloaded.TemplateParams()
	if params.Height != 1 {
		t.Fatalf("template height = %d, want 1", params.Height)
	}
	if params.PrevHash != wantHash {
		t.Fatalf("template prev hash = %x, want %x", params.PrevHash[:8], wantHash[:8])
	}
}

func TestProcessBlockSnapshotDoesNotBlockOnChainWriteLock(t *testing.T) {
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()

	mustAddGenesisBlock(t, chain)

	chain.mu.Lock()
	defer chain.mu.Unlock()
	done := make(chan processBlockSnapshot, 1)
	go func() {
		done <- chain.ProcessBlockSnapshot()
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ProcessBlockSnapshot blocked on chain write lock")
	}
}

func TestProcessBlockSnapshotCapturesRejectedBlock(t *testing.T) {
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()

	mustAddGenesisBlock(t, chain)

	genesis := chain.GetBlockByHeight(0)
	if genesis == nil {
		t.Fatal("expected genesis block at height 0")
	}

	invalid := &Block{
		Header: BlockHeader{
			Version:    1,
			Height:     genesis.Header.Height + 1,
			PrevHash:   genesis.Hash(),
			Timestamp:  genesis.Header.Timestamp + BlockIntervalSec,
			Difficulty: MinDifficulty + 1,
		},
	}

	accepted, isMainChain, err := chain.ProcessBlock(invalid)
	if err == nil {
		t.Fatal("expected invalid block to be rejected")
	}
	if accepted || isMainChain {
		t.Fatalf("unexpected ProcessBlock result accepted=%t main=%t", accepted, isMainChain)
	}

	snapshot := chain.ProcessBlockSnapshot()
	if snapshot.Active {
		t.Fatal("process block snapshot should not remain active after ProcessBlock returns")
	}
	if snapshot.LastHeight != invalid.Header.Height {
		t.Fatalf("last height = %d, want %d", snapshot.LastHeight, invalid.Header.Height)
	}
	if snapshot.LastCompletedAtUnixMillis <= 0 {
		t.Fatal("expected last completed timestamp to be recorded")
	}
	if snapshot.LastTotalMillis > 0 && snapshot.LastValidateMillis > snapshot.LastTotalMillis {
		t.Fatalf(
			"validate millis = %d, total millis = %d",
			snapshot.LastValidateMillis,
			snapshot.LastTotalMillis,
		)
	}
	if snapshot.LastAccepted {
		t.Fatal("rejected block should not be marked accepted in process snapshot")
	}
	if snapshot.LastMainChain {
		t.Fatal("rejected block should not be marked main chain in process snapshot")
	}
	if snapshot.LastError == "" {
		t.Fatal("expected process snapshot to capture the validation error")
	}
}
