package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func checkpointTestDaemon(t *testing.T, height uint64) *Daemon {
	t.Helper()
	chain, _, cleanup := mustCreateTestChain(t)
	t.Cleanup(cleanup)
	// These tests exercise publication, not block validation/PoW.
	for h := uint64(100); h <= height; h += 100 {
		b := &Block{Header: BlockHeader{Height: h, Nonce: h}}
		chain.blocks[b.Hash()] = b
		chain.byHeight[h] = b.Hash()
	}
	chain.height = height
	return &Daemon{chain: chain, saveCheckpoints: true,
		checkpointsFile: filepath.Join(t.TempDir(), "checkpoints.dat")}
}

func TestCheckpointWriterWaitsForFinalityAndUsesCanonicalHash(t *testing.T) {
	d := checkpointTestDaemon(t, 199)
	d.maybeSaveCheckpoints()
	cps, _, max, err := loadCheckpointsFile(d.checkpointsFile)
	if err != nil || len(cps) != 0 || max != 0 {
		t.Fatalf("published unfinalized checkpoint: %v %d %v", cps, max, err)
	}
	// Simulate a reorg of the candidate checkpoint before finalization.
	replacement := &Block{Header: BlockHeader{Height: 100, Nonce: 999}}
	d.chain.blocks[replacement.Hash()] = replacement
	d.chain.byHeight[100] = replacement.Hash()
	d.chain.height = 200
	d.maybeSaveCheckpoints()
	cps, _, max, err = loadCheckpointsFile(d.checkpointsFile)
	if err != nil || len(cps) != 1 || max != 100 || cps[100] != replacement.Hash() {
		t.Fatalf("wrong finalized checkpoint: %v %d %v", cps, max, err)
	}
}

func TestCheckpointWriterRepairsLegacyFileAndBackfills(t *testing.T) {
	d := checkpointTestDaemon(t, 450)
	legacy := "100:" + strings.Repeat("AB", 32) + "\n400:" + strings.Repeat("CD", 32) + "\n"
	if err := os.WriteFile(d.checkpointsFile, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	d.maybeSaveCheckpoints()
	cps, _, max, err := loadCheckpointsFile(d.checkpointsFile)
	if err != nil || len(cps) != 3 || max != 300 {
		t.Fatalf("failed to rebuild legacy file: %v %d %v", cps, max, err)
	}
	for h, hash := range cps {
		if hash != d.chain.GetBlockByHeight(h).Hash() {
			t.Fatalf("noncanonical checkpoint at %d", h)
		}
	}
}

func TestCheckpointWriterMissingBlockPreservesPublishedFile(t *testing.T) {
	d := checkpointTestDaemon(t, 400)
	before := []byte("existing checkpoint file\n")
	if err := os.WriteFile(d.checkpointsFile, before, 0o644); err != nil {
		t.Fatal(err)
	}
	delete(d.chain.byHeight, 200)
	if _, _, _, err := d.saveCanonicalCheckpoints(d.checkpointsFile, true); err == nil {
		t.Fatal("expected missing canonical block error")
	}
	got, err := os.ReadFile(d.checkpointsFile)
	if err != nil || string(got) != string(before) || d.checkpointsInitialized {
		t.Fatalf("failed write changed publication: %q %v", got, err)
	}
}

func TestCheckpointWriterRetriesFailedPublication(t *testing.T) {
	d := checkpointTestDaemon(t, 300)
	// A directory at the destination makes atomic replacement fail.
	if err := os.Mkdir(d.checkpointsFile, 0o755); err != nil {
		t.Fatal(err)
	}
	d.maybeSaveCheckpoints()
	if d.checkpointsInitialized || d.lastCheckpointWritten != 0 {
		t.Fatal("writer advanced after failed publication")
	}
	if err := os.Remove(d.checkpointsFile); err != nil {
		t.Fatal(err)
	}
	d.maybeSaveCheckpoints()
	if !d.checkpointsInitialized || d.lastCheckpointWritten != 200 {
		t.Fatal("writer did not retry at the same height")
	}
}

func TestCheckpointWriterRetainsAnchorsWhileSyncing(t *testing.T) {
	d := checkpointTestDaemon(t, 300)
	d.chain.fastSyncUntilHeight = 1000
	before := []byte("1000:" + strings.Repeat("AB", 32) + "\n")
	if err := os.WriteFile(d.checkpointsFile, before, 0o644); err != nil {
		t.Fatal(err)
	}
	d.maybeSaveCheckpoints()
	got, err := os.ReadFile(d.checkpointsFile)
	if err != nil || string(got) != string(before) {
		t.Fatalf("lost downloaded anchors: %q %v", got, err)
	}
}

func TestCheckpointSaveAPIRepairsWithoutWaitingForNextInterval(t *testing.T) {
	d := checkpointTestDaemon(t, 350)
	d.maybeSaveCheckpoints()
	if err := os.WriteFile(d.checkpointsFile, []byte("100:"+strings.Repeat("AB", 32)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewAPIServer(d, nil, nil, filepath.Dir(d.checkpointsFile), nil)
	rr := mustMakeHTTPJSONRequest(t, http.HandlerFunc(s.handleSaveCheckpoints), http.MethodPost, "/api/checkpoints/save", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("save failed: %d %s", rr.Code, rr.Body.String())
	}
	cps, _, max, err := loadCheckpointsFile(d.checkpointsFile)
	if err != nil || len(cps) != 2 || max != 200 || cps[100] != d.chain.GetBlockByHeight(100).Hash() {
		t.Fatalf("API did not repair stale file: %v %d %v", cps, max, err)
	}
}

func TestCheckpointWritersSerializeAndReadersSeeCompleteFile(t *testing.T) {
	d := checkpointTestDaemon(t, 400)
	d.maybeSaveCheckpoints()
	var expected strings.Builder
	for h := uint64(100); h <= 300; h += 100 {
		fmt.Fprintf(&expected, "%d:%X\n", h, d.chain.GetBlockByHeight(h).Hash())
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				if _, _, _, err := d.saveCanonicalCheckpoints(d.checkpointsFile, true); err != nil {
					t.Error(err)
				}
				body, err := os.ReadFile(d.checkpointsFile)
				if err != nil || string(body) != expected.String() {
					t.Errorf("partial or unordered file: %q %v", body, err)
				}
			}
		}()
	}
	wg.Wait()
}
