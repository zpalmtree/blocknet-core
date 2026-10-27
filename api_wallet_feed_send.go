package main

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"blocknet/protocol/params"
	"blocknet/wallet"
)

// Wallet feed, send side: what an external signer needs to build and submit
// transactions without holding chain state (decoy selection, fee/ring
// parameters, key-image status, raw submission). Private routes only.

const (
	walletFeedMaxRingInputs = 16
	walletFeedMaxKeyImages  = 256
	walletFeedMaxTxBytes    = 1 << 20
)

// handleWalletFeedParams serves GET /api/wallet-feed/params.
func (s *APIServer) handleWalletFeedParams(w http.ResponseWriter, r *http.Request) {
	network := "mainnet"
	if params.IsTestnet {
		network = "testnet"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"network":                   network,
		"ring_size":                 RingSize,
		"min_fee":                   sendMinFee,
		"fee_per_byte":              sendFeePerByte,
		"indexed_derivation_height": params.IndexedDerivationHeight,
		"coinbase_maturity":         wallet.CoinbaseMaturity,
		"safe_confirmations":        wallet.SafeConfirmations,
	})
}

func decodeHex32(s string) ([32]byte, bool) {
	var out [32]byte
	if len(s) != 64 {
		return out, false
	}
	if _, err := hex.Decode(out[:], []byte(s)); err != nil {
		return out, false
	}
	return out, true
}

func encodeHex32s(in [][32]byte) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = hex.EncodeToString(in[i][:])
	}
	return out
}

// handleWalletFeedRingMembers serves POST /api/wallet-feed/ring-members:
// canonical decoys for each real input, exactly as the in-daemon wallet
// selects them.
func (s *APIServer) handleWalletFeedRingMembers(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Inputs []struct {
			PublicKey  string `json:"public_key"`
			Commitment string `json:"commitment"`
		} `json:"inputs"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || len(req.Inputs) == 0 || len(req.Inputs) > walletFeedMaxRingInputs {
		writeError(w, http.StatusBadRequest, "inputs must hold 1..16 {public_key, commitment}")
		return
	}
	type ring struct {
		Keys        []string `json:"keys"`
		Commitments []string `json:"commitments"`
		SecretIndex int      `json:"secret_index"`
	}
	out := make([]ring, len(req.Inputs))
	for i, in := range req.Inputs {
		pub, ok1 := decodeHex32(in.PublicKey)
		commitment, ok2 := decodeHex32(in.Commitment)
		if !ok1 || !ok2 {
			writeError(w, http.StatusBadRequest, "public_key and commitment must be 64 hex characters")
			return
		}
		data, err := s.daemon.Chain().SelectRingMembersWithCommitments(pub, commitment)
		if err != nil {
			writeError(w, http.StatusConflict, "ring selection failed: "+err.Error())
			return
		}
		out[i] = ring{Keys: encodeHex32s(data.Keys), Commitments: encodeHex32s(data.Commitments), SecretIndex: data.SecretIndex}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rings": out})
}

// handleWalletFeedKeyImages serves POST /api/wallet-feed/key-images.
func (s *APIServer) handleWalletFeedKeyImages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyImages []string `json:"key_images"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || len(req.KeyImages) == 0 || len(req.KeyImages) > walletFeedMaxKeyImages {
		writeError(w, http.StatusBadRequest, "key_images must hold 1..256 hex values")
		return
	}
	type status struct {
		KeyImage  string `json:"key_image"`
		Spent     bool   `json:"spent"`
		InMempool bool   `json:"in_mempool"`
	}
	out := make([]status, len(req.KeyImages))
	for i, raw := range req.KeyImages {
		ki, ok := decodeHex32(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "key images must be 64 hex characters")
			return
		}
		out[i] = status{KeyImage: raw, Spent: s.daemon.Chain().IsKeyImageSpent(ki), InMempool: s.daemon.Mempool().HasKeyImage(ki)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"key_images": out})
}

// handleWalletFeedSubmit serves POST /api/wallet-feed/submit. Submitting the
// same bytes again is idempotent: a transaction already in the mempool or on
// the canonical chain is reported as such instead of as a failure.
func (s *APIServer) handleWalletFeedSubmit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tx string `json:"tx"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*walletFeedMaxTxBytes+1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be {\"tx\": <hex>}")
		return
	}
	txData, err := hex.DecodeString(req.Tx)
	if err != nil || len(txData) == 0 || len(txData) > walletFeedMaxTxBytes {
		writeError(w, http.StatusBadRequest, "tx must be non-empty hex of at most 1 MiB")
		return
	}
	tx, err := DeserializeTx(txData)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid transaction data")
		return
	}
	txID, err := tx.TxID()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid transaction data")
		return
	}
	idHex := hex.EncodeToString(txID[:])
	if s.daemon.Mempool().HasTransaction(txID) {
		writeJSON(w, http.StatusOK, map[string]string{"tx_id": idHex, "status": "mempool"})
		return
	}
	if found, height, err := s.daemon.Chain().Storage().FindCanonicalTransaction(txID); err == nil && found != nil {
		writeJSON(w, http.StatusOK, map[string]any{"tx_id": idHex, "status": "canonical", "height": height})
		return
	}
	if err := s.daemon.SubmitTransaction(txData); err != nil {
		// A full mempool is transient; anything else is a rejection of
		// these exact bytes.
		status := http.StatusUnprocessableEntity
		if strings.Contains(err.Error(), "mempool full") {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{"tx_id": idHex, "status": "rejected", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tx_id": idHex, "status": "accepted"})
}
