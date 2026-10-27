package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"blocknet/protocol/params"
	"blocknet/wallet"
)

func feedPost(t *testing.T, mux http.Handler, path string, body any, out any) int {
	t.Helper()
	raw, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", path, bytes.NewReader(raw)))
	if out != nil {
		_ = json.Unmarshal(rr.Body.Bytes(), out)
	}
	return rr.Code
}

func scanDataFor(b *Block) *wallet.BlockData {
	bd := &wallet.BlockData{Height: b.Header.Height, Hash: b.Hash()}
	for i, tx := range b.Transactions {
		id, _ := tx.TxID()
		td := wallet.TxData{TxID: id, TxPubKey: tx.TxPublicKey, IsCoinbase: i == 0 && b.Header.Height > 0}
		for j, o := range tx.Outputs {
			td.Outputs = append(td.Outputs, wallet.OutputData{Index: j, PubKey: o.PublicKey, Commitment: o.Commitment, EncryptedAmount: o.EncryptedAmount, EncryptedMemo: o.EncryptedMemo})
		}
		for _, in := range tx.Inputs {
			td.KeyImages = append(td.KeyImages, in.KeyImage)
		}
		bd.Transactions = append(bd.Transactions, td)
	}
	return bd
}

// walletFeedTestnet switches to testnet params for one test.
func walletFeedTestnet(t *testing.T) {
	t.Helper()
	testnet, network, chainID, indexed, memo := params.IsTestnet, params.NetworkID, params.ChainID, params.IndexedDerivationHeight, params.MemoBlockDomainSep
	base, pex, block, tx, sync, dan := params.P2PProtocolBase, params.ProtocolPEX, params.ProtocolBlock, params.ProtocolTx, params.ProtocolSync, params.ProtocolDandelion
	t.Cleanup(func() {
		params.IsTestnet, params.NetworkID, params.ChainID, params.IndexedDerivationHeight, params.MemoBlockDomainSep = testnet, network, chainID, indexed, memo
		params.P2PProtocolBase, params.ProtocolPEX, params.ProtocolBlock, params.ProtocolTx, params.ProtocolSync, params.ProtocolDandelion = base, pex, block, tx, sync, dan
	})
	params.InitTestnet()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// An external signer builds a real RingCT spend using only the feed: decoys
// from /ring-members, fee/ring parameters from /params, then submits it.
func TestWalletFeedExternalSignerRoundTrip(t *testing.T) {
	walletFeedTestnet(t)
	chain, _, cleanup := mustCreateTestChain(t)
	t.Cleanup(cleanup)
	mustAddGenesisBlock(t, chain)
	d, stop := mustStartTestDaemon(t, chain)
	t.Cleanup(stop)
	api := NewAPIServer(d, nil, nil, t.TempDir(), nil)
	mux := http.NewServeMux()
	api.registerPrivateRoutes(mux)

	keys, err := GenerateStealthKeys()
	if err != nil {
		t.Fatal(err)
	}
	other, err := GenerateStealthKeys()
	if err != nil {
		t.Fatal(err)
	}
	sk := wallet.StealthKeys{SpendPrivKey: keys.SpendPrivKey, SpendPubKey: keys.SpendPubKey, ViewPrivKey: keys.ViewPrivKey, ViewPubKey: keys.ViewPubKey}
	w, err := wallet.NewWalletFromStealthKeys(filepath.Join(t.TempDir(), "w.dat"), []byte("feed-test-password"), sk, defaultWalletConfig())
	if err != nil {
		t.Fatal(err)
	}
	scanner := wallet.NewScanner(w, defaultScannerConfig())
	for h := uint64(1); h <= 65; h++ {
		decoy, err := CreateCoinbase(other.SpendPubKey, other.ViewPubKey, 50_000_000, h)
		if err != nil {
			t.Fatal(err)
		}
		txs := []*Transaction{decoy.Tx}
		if h == 1 {
			reward, err := CreateCoinbase(keys.SpendPubKey, keys.ViewPubKey, 100_000_000, h)
			if err != nil {
				t.Fatal(err)
			}
			txs = []*Transaction{reward.Tx, decoy.Tx}
		}
		scanner.ScanBlock(scanDataFor(walletFeedAppendBlock(t, chain, txs...)))
	}

	var p struct {
		RingSize   int    `json:"ring_size"`
		MinFee     uint64 `json:"min_fee"`
		FeePerByte uint64 `json:"fee_per_byte"`
		Network    string `json:"network"`
	}
	if code := feedGet(t, mux, "/api/wallet-feed/params", &p); code != 200 || p.RingSize != RingSize || p.MinFee != sendMinFee || p.FeePerByte != sendFeePerByte || p.Network != "testnet" {
		t.Fatalf("params %d %+v", code, p)
	}

	ringCalls := 0
	cfg := wallet.TransferConfig{
		SelectRingMembers: func(pub, commitment [32]byte) ([][32]byte, [][32]byte, int, error) {
			ringCalls++
			var resp struct {
				Rings []struct {
					Keys        []string `json:"keys"`
					Commitments []string `json:"commitments"`
					SecretIndex int      `json:"secret_index"`
				} `json:"rings"`
			}
			body := map[string]any{"inputs": []map[string]string{{"public_key": hex.EncodeToString(pub[:]), "commitment": hex.EncodeToString(commitment[:])}}}
			if code := feedPost(t, mux, "/api/wallet-feed/ring-members", body, &resp); code != 200 || len(resp.Rings) != 1 {
				t.Fatalf("ring-members HTTP %d", code)
			}
			r := resp.Rings[0]
			var ks, cs [][32]byte
			for i := range r.Keys {
				k, _ := decodeHex32(r.Keys[i])
				c, _ := decodeHex32(r.Commitments[i])
				ks, cs = append(ks, k), append(cs, c)
			}
			return ks, cs, r.SecretIndex, nil
		},
		CreateCommitment: func(amount uint64, blinding [32]byte) [32]byte {
			c, _ := CreatePedersenCommitmentWithBlinding(amount, blinding)
			return c
		},
		CreateRangeProof: func(amount uint64, blinding [32]byte) ([]byte, error) {
			proof, err := CreateRangeProof(amount, blinding)
			if err != nil {
				return nil, err
			}
			return proof.Proof, nil
		},
		SignRingCT: func(ringKeys, ringCommitments [][32]byte, secretIndex int, privateKey, realBlinding, pseudoCommitment, pseudoBlinding [32]byte, message []byte) ([]byte, [32]byte, error) {
			sig, err := SignRingCT(ringKeys, ringCommitments, secretIndex, privateKey, realBlinding, pseudoCommitment, pseudoBlinding, message)
			if err != nil {
				return nil, [32]byte{}, err
			}
			return sig.Signature, sig.KeyImage, nil
		},
		GenerateBlinding: func() [32]byte {
			b, _ := GenerateBlinding()
			return b
		},
		ComputeTxID: func(txData []byte) ([32]byte, error) {
			tx, err := DeserializeTx(txData)
			if err != nil {
				return [32]byte{}, err
			}
			return tx.TxID()
		},
		DeriveStealthAddress: func(spendPub, viewPub [32]byte) (txPriv, txPub, oneTimePub [32]byte, err error) {
			o, err := DeriveStealthAddress(spendPub, viewPub)
			if err != nil {
				return txPriv, txPub, oneTimePub, err
			}
			return o.TxPrivKey, o.TxPubKey, o.OnetimePubKey, nil
		},
		DeriveStealthAddressWithKey: DeriveStealthAddressWithKey,
		DeriveDeterministicTxKey:    DeriveDeterministicTxKey,
		GenerateKeyImage:            GenerateKeyImage,
		DeriveSharedSecret:          DeriveStealthSecretSender,
		DeriveSharedSecretIndexed:   DeriveStealthSecretSenderIndexed,
		ScalarToPoint:               ScalarToPubKey,
		PointAdd:                    func(a, b [32]byte) ([32]byte, error) { return CommitmentAdd(a, b) },
		BlindingAdd:                 BlindingAdd,
		BlindingSub:                 BlindingSub,
		RingSize:                    p.RingSize,
		MinFee:                      p.MinFee,
		FeePerByte:                  p.FeePerByte,
	}
	res, err := wallet.NewBuilder(w, cfg).Transfer([]wallet.Recipient{{SpendPubKey: other.SpendPubKey, ViewPubKey: other.ViewPubKey, Amount: 20_000_000}}, p.FeePerByte, chain.Height())
	if err != nil {
		t.Fatalf("build spend: %v", err)
	}
	if ringCalls == 0 {
		t.Fatal("builder did not use the ring-members endpoint")
	}
	txHex := hex.EncodeToString(res.TxData)
	idHex := hex.EncodeToString(res.TxID[:])
	spent, _ := DeserializeTx(res.TxData)
	ki := hex.EncodeToString(spent.Inputs[0].KeyImage[:])

	var sub map[string]any
	if code := feedPost(t, mux, "/api/wallet-feed/submit", map[string]string{"tx": txHex}, &sub); code != 200 || sub["status"] != "accepted" || sub["tx_id"] != idHex {
		t.Fatalf("submit: %d %v", code, sub)
	}
	if code := feedPost(t, mux, "/api/wallet-feed/submit", map[string]string{"tx": txHex}, &sub); code != 200 || sub["status"] != "mempool" {
		t.Fatalf("resubmit: %d %v", code, sub)
	}
	var kis struct {
		KeyImages []struct {
			Spent     bool `json:"spent"`
			InMempool bool `json:"in_mempool"`
		} `json:"key_images"`
	}
	if feedPost(t, mux, "/api/wallet-feed/key-images", map[string][]string{"key_images": {ki}}, &kis); len(kis.KeyImages) != 1 || kis.KeyImages[0].Spent || !kis.KeyImages[0].InMempool {
		t.Fatalf("key image status before inclusion: %+v", kis)
	}

	// Mine it; resubmission now reports the canonical inclusion.
	d.mempool.RemoveByKeyImage(spent.Inputs[0].KeyImage)
	walletFeedAppendBlock(t, chain, spent)
	if code := feedPost(t, mux, "/api/wallet-feed/submit", map[string]string{"tx": txHex}, &sub); code != 200 || sub["status"] != "canonical" {
		t.Fatalf("submit after inclusion: %d %v", code, sub)
	}
	if feedPost(t, mux, "/api/wallet-feed/key-images", map[string][]string{"key_images": {ki}}, &kis); !kis.KeyImages[0].Spent {
		t.Fatalf("key image not spent after inclusion: %+v", kis)
	}

	// Rejections.
	if code := feedPost(t, mux, "/api/wallet-feed/submit", map[string]string{"tx": "zz"}, nil); code != http.StatusBadRequest {
		t.Fatalf("bad hex: %d", code)
	}
	if code := feedPost(t, mux, "/api/wallet-feed/submit", map[string]string{"tx": "00ff"}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("garbage tx: %d", code)
	}
	tampered := append([]byte(nil), res.TxData...)
	tampered[len(tampered)-1] ^= 1
	if code := feedPost(t, mux, "/api/wallet-feed/submit", map[string]string{"tx": hex.EncodeToString(tampered)}, &sub); code != http.StatusUnprocessableEntity || sub["status"] != "rejected" {
		t.Fatalf("tampered tx: %d %v", code, sub)
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"inputs": []any{}}, map[string]any{"inputs": []map[string]string{{"public_key": "x", "commitment": "y"}}}} {
		if code := feedPost(t, mux, "/api/wallet-feed/ring-members", bad, nil); code != http.StatusBadRequest {
			t.Fatalf("ring-members accepted %v: %d", bad, code)
		}
	}
	if code := feedPost(t, mux, "/api/wallet-feed/key-images", map[string][]string{"key_images": {"nothex"}}, nil); code != http.StatusBadRequest {
		t.Fatalf("bad key image accepted: %d", code)
	}
}
