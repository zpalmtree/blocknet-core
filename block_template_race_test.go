package main

import (
	"runtime"
	"sync"
	"testing"

	"golang.org/x/crypto/sha3"
)

func TestChainTemplateParams_ConcurrentCanonicalIndexRefreshDoesNotPanic(t *testing.T) {
	t.Setenv("BLOCKNET_CHAIN_CACHE_CAP", "164")

	dataDir := t.TempDir()

	chain, err := NewChain(dataDir)
	if err != nil {
		t.Fatalf("failed to create chain: %v", err)
	}
	defer func() {
		if chain != nil {
			if err := chain.Close(); err != nil {
				t.Fatalf("failed to close chain: %v", err)
			}
		}
	}()

	mustAddGenesisBlock(t, chain)

	st := chain.Storage()
	tipHash, tipHeight, tipWork, found := st.GetTip()
	if !found || tipHeight != 0 {
		t.Fatalf("expected genesis tip at height 0 (found=%v height=%d)", found, tipHeight)
	}

	pub, err := GenerateRistrettoKeypair()
	if err != nil {
		t.Fatalf("failed to create output pubkey: %v", err)
	}
	commit, err := GenerateRistrettoKeypair()
	if err != nil {
		t.Fatalf("failed to create output commitment: %v", err)
	}

	targetPub := pub.PublicKey
	targetComm := commit.PublicKey
	prevHash := tipHash
	prevWork := tipWork
	prevTimestamp := GenesisTimestamp

	for h := uint64(1); h <= 400; h++ {
		var txs []*Transaction
		var newOutputs []*UTXO
		if h == 1 {
			tx := &Transaction{
				Version: 1,
				Outputs: []TxOutput{
					{
						PublicKey:  targetPub,
						Commitment: targetComm,
					},
				},
			}
			txid, err := tx.TxID()
			if err != nil {
				t.Fatalf("failed to compute txid at height %d: %v", h, err)
			}
			txs = []*Transaction{tx}
			newOutputs = []*UTXO{
				{
					TxID:        txid,
					OutputIndex: 0,
					Output:      tx.Outputs[0],
					BlockHeight: h,
				},
			}
		}

		block := &Block{
			Header: BlockHeader{
				Version:    1,
				Height:     h,
				PrevHash:   prevHash,
				MerkleRoot: sha3.Sum256([]byte{0xB0, byte(h), byte(h >> 8)}),
				Timestamp:  prevTimestamp + BlockIntervalSec,
				Difficulty: MinDifficulty,
			},
			Transactions: txs,
		}
		hash := block.Hash()
		work, err := addCumulativeWork(prevWork, block.Header.Difficulty)
		if err != nil {
			t.Fatalf("failed to compute cumulative work at height %d: %v", h, err)
		}
		if err := st.CommitBlock(&BlockCommit{
			Block:      block,
			Height:     h,
			Hash:       hash,
			Work:       work,
			IsMainTip:  true,
			NewOutputs: newOutputs,
		}); err != nil {
			t.Fatalf("failed to commit block at height %d: %v", h, err)
		}

		prevHash = hash
		prevWork = work
		prevTimestamp = block.Header.Timestamp
	}

	if err := chain.Close(); err != nil {
		t.Fatalf("failed to close chain before restart: %v", err)
	}
	chain = nil

	chain, err = NewChain(dataDir)
	if err != nil {
		t.Fatalf("failed to restart chain: %v", err)
	}
	if got := chain.Height(); got != 400 {
		t.Fatalf("unexpected restarted height: got %d want 400", got)
	}

	targetHash, ok := chain.Storage().GetBlockHashByHeight(1)
	if !ok {
		t.Fatal("missing hash for height 1")
	}
	chain.mu.RLock()
	_, cached := chain.blocks[targetHash]
	chain.mu.RUnlock()
	if cached {
		t.Fatal("expected height-1 block to be storage-backed after restart")
	}

	oldProcs := runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(oldProcs)

	start := make(chan struct{})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				select {
				case <-stop:
					return
				default:
					_ = chain.TemplateParams()
				}
			}
		}()
	}

	close(start)
	for i := 0; i < 64; i++ {
		if !chain.IsCanonicalRingMember(targetPub, targetComm) {
			close(stop)
			wg.Wait()
			t.Fatal("expected historical output to remain canonical")
		}
		chain.mu.Lock()
		chain.canonicalRingIndexDirty = true
		chain.mu.Unlock()
	}
	close(stop)
	wg.Wait()
}
