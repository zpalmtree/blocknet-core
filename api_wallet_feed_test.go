package main

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// walletFeedAppendBlock connects a block without PoW (deterministic fixture).
func walletFeedAppendBlock(t *testing.T, chain *Chain, txs ...*Transaction) *Block {
	t.Helper()
	prev := chain.GetBlockByHeight(chain.Height())
	height := chain.Height() + 1
	block := &Block{Header: BlockHeader{Version: 1, Height: height, PrevHash: prev.Hash(), Timestamp: prev.Header.Timestamp + BlockIntervalSec, Difficulty: MinDifficulty}, Transactions: txs}
	var err error
	if block.Header.MerkleRoot, err = block.ComputeMerkleRoot(); err != nil {
		t.Fatal(err)
	}
	var spent [][32]byte
	for _, tx := range txs {
		for _, in := range tx.Inputs {
			spent = append(spent, in.KeyImage)
		}
	}
	if err := chain.Storage().CommitBlock(&BlockCommit{Block: block, Height: height, Hash: block.Hash(), Work: height + 1, IsMainTip: true, NewOutputs: blockOutputsForCommit(t, block), SpentKeyImgs: spent}); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	chain.blocks[block.Hash()] = block
	chain.workAt[block.Hash()] = height + 1
	chain.byHeight[height] = block.Hash()
	chain.bestHash = block.Hash()
	chain.height = height
	chain.totalWork = height + 1
	chain.canonicalRingIndexDirty = true
	chain.updateTipSnapshotLocked()
	chain.mu.Unlock()
	return block
}

type feedPage struct {
	Tip struct {
		Height uint64 `json:"height"`
		Hash   string `json:"hash"`
	} `json:"tip"`
	Blocks []walletFeedBlock `json:"blocks"`
}

func walletFeedFixture(t *testing.T) (*Chain, *Daemon, *http.ServeMux, []*Block) {
	t.Helper()
	chain, _, cleanup := mustCreateTestChain(t)
	t.Cleanup(cleanup)
	mustAddGenesisBlock(t, chain)
	d, dCleanup := mustStartTestDaemon(t, chain)
	t.Cleanup(dCleanup)
	keys, err := GenerateStealthKeys()
	if err != nil {
		t.Fatal(err)
	}
	var blocks []*Block
	for h := uint64(1); h <= 3; h++ {
		cb, err := CreateCoinbase(keys.SpendPubKey, keys.ViewPubKey, 1000+h, h)
		if err != nil {
			t.Fatal(err)
		}
		txs := []*Transaction{cb.Tx}
		if h == 2 {
			other, err := CreateCoinbase(keys.SpendPubKey, keys.ViewPubKey, 7, h)
			if err != nil {
				t.Fatal(err)
			}
			spend := *other.Tx
			spend.Inputs = []TxInput{{KeyImage: [32]byte{0xaa}}, {KeyImage: [32]byte{0xbb}}}
			txs = append(txs, &spend)
		}
		blocks = append(blocks, walletFeedAppendBlock(t, chain, txs...))
	}
	api := NewAPIServer(d, nil, nil, t.TempDir(), nil)
	mux := http.NewServeMux()
	api.registerPrivateRoutes(mux)
	return chain, d, mux, blocks
}

func feedGet(t *testing.T, mux http.Handler, path string, out any) int {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	if out != nil && rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	return rr.Code
}

func TestWalletFeedBlocksMatchChain(t *testing.T) {
	chain, _, mux, blocks := walletFeedFixture(t)
	var p feedPage
	if code := feedGet(t, mux, "/api/wallet-feed/blocks?from=1&limit=2", &p); code != 200 {
		t.Fatalf("HTTP %d", code)
	}
	tip := chain.BestHash()
	if p.Tip.Height != 3 || p.Tip.Hash != hex.EncodeToString(tip[:]) || len(p.Blocks) != 2 {
		t.Fatalf("bad page header: %+v", p.Tip)
	}
	for i, fb := range p.Blocks {
		b := blocks[i]
		hash := b.Hash()
		if fb.Height != b.Header.Height || fb.Hash != hex.EncodeToString(hash[:]) || fb.PrevHash != hex.EncodeToString(b.Header.PrevHash[:]) || fb.Timestamp != b.Header.Timestamp {
			t.Fatalf("block %d header mismatch", fb.Height)
		}
		if i > 0 && fb.PrevHash != p.Blocks[i-1].Hash {
			t.Fatal("page is not hash-linked")
		}
		if len(fb.Transactions) != len(b.Transactions) {
			t.Fatalf("block %d: %d txs, want %d", fb.Height, len(fb.Transactions), len(b.Transactions))
		}
		for j, ftx := range fb.Transactions {
			tx := b.Transactions[j]
			id, _ := tx.TxID()
			if ftx.TxID != hex.EncodeToString(id[:]) || ftx.TxPublicKey != hex.EncodeToString(tx.TxPublicKey[:]) || ftx.IsCoinbase != (j == 0) {
				t.Fatalf("tx %d/%d identity mismatch", fb.Height, j)
			}
			if len(ftx.KeyImages) != len(tx.Inputs) || len(ftx.Outputs) != len(tx.Outputs) {
				t.Fatalf("tx %d/%d shape mismatch", fb.Height, j)
			}
			for k, ki := range ftx.KeyImages {
				if ki != hex.EncodeToString(tx.Inputs[k].KeyImage[:]) {
					t.Fatal("key image mismatch")
				}
			}
			for k, o := range ftx.Outputs {
				src := tx.Outputs[k]
				if o.Index != k || o.PublicKey != hex.EncodeToString(src.PublicKey[:]) || o.Commitment != hex.EncodeToString(src.Commitment[:]) ||
					o.EncryptedAmount != hex.EncodeToString(src.EncryptedAmount[:]) || o.EncryptedMemo != hex.EncodeToString(src.EncryptedMemo[:]) ||
					len(o.EncryptedAmount) != 16 || len(o.EncryptedMemo) != 2*len(src.EncryptedMemo) {
					t.Fatalf("output %d/%d/%d mismatch", fb.Height, j, k)
				}
			}
		}
	}
	if len(p.Blocks[1].Transactions[1].KeyImages) != 2 {
		t.Fatal("spend key images missing")
	}

	p = feedPage{}
	if code := feedGet(t, mux, "/api/wallet-feed/blocks?from=9", &p); code != 200 || len(p.Blocks) != 0 || p.Blocks == nil || p.Tip.Height != 3 {
		t.Fatalf("beyond-tip page: code %d %+v", code, p)
	}
	for _, bad := range []string{"", "?from=x", "?from=1&limit=0", "?from=1&limit=101", "?from=-1", "?from=1&limit=z"} {
		if code := feedGet(t, mux, "/api/wallet-feed/blocks"+bad, nil); code != http.StatusBadRequest {
			t.Fatalf("%q: HTTP %d, want 400", bad, code)
		}
	}
}

func TestWalletFeedTxStatus(t *testing.T) {
	_, d, mux, blocks := walletFeedFixture(t)
	cb := blocks[1].Transactions[0]
	id, _ := cb.TxID()
	var st walletFeedTxStatus
	if code := feedGet(t, mux, "/api/wallet-feed/tx/"+hex.EncodeToString(id[:]), &st); code != 200 {
		t.Fatalf("HTTP %d", code)
	}
	bh := blocks[1].Hash()
	if st.Status != "canonical" || st.Height != 2 || st.BlockHash != hex.EncodeToString(bh[:]) || st.Tx == nil || !st.Tx.IsCoinbase {
		t.Fatalf("canonical coinbase status %+v", st)
	}
	spend := blocks[1].Transactions[1]
	sid, _ := spend.TxID()
	st = walletFeedTxStatus{}
	feedGet(t, mux, "/api/wallet-feed/tx/"+hex.EncodeToString(sid[:]), &st)
	if st.Status != "canonical" || st.Tx.IsCoinbase || len(st.Tx.KeyImages) != 2 {
		t.Fatalf("canonical spend status %+v", st)
	}

	st = walletFeedTxStatus{}
	feedGet(t, mux, "/api/wallet-feed/tx/"+hex.EncodeToString(make([]byte, 32)), &st)
	if st.Status != "unknown" || st.Tx != nil {
		t.Fatalf("unknown tx status %+v", st)
	}

	pending := &Transaction{Version: 1, Fee: 9, Inputs: []TxInput{{KeyImage: [32]byte{0xcc}}}, Outputs: []TxOutput{{}}}
	pid, _ := pending.TxID()
	d.mempool.mu.Lock()
	d.mempool.txByID[pid] = &MempoolEntry{Tx: pending, TxID: pid, TxData: pending.Serialize(), Fee: pending.Fee, FeeRate: 1, Size: pending.Size()}
	d.mempool.mu.Unlock()
	st = walletFeedTxStatus{}
	feedGet(t, mux, "/api/wallet-feed/tx/"+hex.EncodeToString(pid[:]), &st)
	if st.Status != "mempool" || st.Tx == nil || st.Height != 0 {
		t.Fatalf("mempool status %+v", st)
	}
	if code := feedGet(t, mux, "/api/wallet-feed/tx/zz", nil); code != http.StatusBadRequest {
		t.Fatalf("bad txid: HTTP %d", code)
	}
}

func TestWalletFeedIsPrivateOnly(t *testing.T) {
	_, d, _, _ := walletFeedFixture(t)
	api := NewAPIServer(d, nil, nil, t.TempDir(), nil)
	public := http.NewServeMux()
	api.registerPublicRoutes(public)
	if code := feedGet(t, public, "/api/wallet-feed/blocks?from=0", nil); code != http.StatusNotFound {
		t.Fatalf("feed exposed on the public (explorer) mux: HTTP %d", code)
	}
}

func TestWalletFeedPagesConsistentDuringGrowth(t *testing.T) {
	chain, _, mux, _ := walletFeedFixture(t)
	keys, err := GenerateStealthKeys()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for h := uint64(4); h <= 40; h++ {
			cb, err := CreateCoinbase(keys.SpendPubKey, keys.ViewPubKey, h, h)
			if err != nil {
				t.Error(err)
				return
			}
			walletFeedAppendBlock(t, chain, cb.Tx)
		}
		close(stop)
	})
	for done := false; !done; {
		select {
		case <-stop:
			done = true
		default:
		}
		var p feedPage
		if code := feedGet(t, mux, "/api/wallet-feed/blocks?from=1&limit=100", &p); code != 200 {
			t.Fatalf("HTTP %d", code)
		}
		if uint64(len(p.Blocks)) != p.Tip.Height {
			t.Fatalf("page has %d blocks for tip %d", len(p.Blocks), p.Tip.Height)
		}
		for i := 1; i < len(p.Blocks); i++ {
			if p.Blocks[i].PrevHash != p.Blocks[i-1].Hash {
				t.Fatal("page not hash-linked")
			}
		}
		if last := p.Blocks[len(p.Blocks)-1]; last.Hash != p.Tip.Hash {
			t.Fatal("last block is not the reported tip")
		}
	}
	wg.Wait()
}
