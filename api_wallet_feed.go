package main

import (
	"encoding/hex"
	"net/http"
	"strconv"
)

// Wallet feed: canonical scan data for external wallet services such as the
// hosted wallet (contract: showcases/hosted-wallet/m1/contracts.md §1).
// Authenticated, read-only, and served from one canonical snapshot per page.

const (
	walletFeedDefaultLimit = 50
	walletFeedMaxLimit     = 100
)

type walletFeedOutput struct {
	Index           int    `json:"index"`
	PublicKey       string `json:"public_key"`
	Commitment      string `json:"commitment"`
	EncryptedAmount string `json:"encrypted_amount"`
	EncryptedMemo   string `json:"encrypted_memo"`
}

type walletFeedTx struct {
	TxID        string             `json:"tx_id"`
	TxPublicKey string             `json:"tx_public_key"`
	IsCoinbase  bool               `json:"is_coinbase"`
	Outputs     []walletFeedOutput `json:"outputs"`
	KeyImages   []string           `json:"key_images"`
}

type walletFeedBlock struct {
	Height       uint64         `json:"height"`
	Hash         string         `json:"hash"`
	PrevHash     string         `json:"prev_hash"`
	Timestamp    int64          `json:"timestamp"`
	Transactions []walletFeedTx `json:"transactions"`
}

func walletFeedTxJSON(tx *Transaction, isCoinbase bool) (walletFeedTx, error) {
	id, err := tx.TxID()
	if err != nil {
		return walletFeedTx{}, err
	}
	out := walletFeedTx{
		TxID:        hex.EncodeToString(id[:]),
		TxPublicKey: hex.EncodeToString(tx.TxPublicKey[:]),
		IsCoinbase:  isCoinbase,
		Outputs:     make([]walletFeedOutput, len(tx.Outputs)),
		KeyImages:   make([]string, len(tx.Inputs)),
	}
	for i := range tx.Outputs {
		o := &tx.Outputs[i]
		out.Outputs[i] = walletFeedOutput{
			Index:           i,
			PublicKey:       hex.EncodeToString(o.PublicKey[:]),
			Commitment:      hex.EncodeToString(o.Commitment[:]),
			EncryptedAmount: hex.EncodeToString(o.EncryptedAmount[:]),
			EncryptedMemo:   hex.EncodeToString(o.EncryptedMemo[:]),
		}
	}
	for i := range tx.Inputs {
		out.KeyImages[i] = hex.EncodeToString(tx.Inputs[i].KeyImage[:])
	}
	return out, nil
}

// CanonicalRange returns up to limit main-chain blocks from height from,
// together with the tip they belong to, all read under one chain lock so the
// page is internally consistent.
func (c *Chain) CanonicalRange(from uint64, limit int) (tipHeight uint64, tipHash [32]byte, blocks []*Block) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tipHeight, tipHash = c.height, c.bestHash
	if from > tipHeight || limit <= 0 {
		return tipHeight, tipHash, nil
	}
	end := tipHeight
	if span := uint64(limit - 1); from+span < end {
		end = from + span
	}
	blocks = make([]*Block, 0, end-from+1)
	for h := from; h <= end; h++ {
		b := c.getBlockByHeightLocked(h)
		if b == nil {
			break
		}
		blocks = append(blocks, b)
	}
	return tipHeight, tipHash, blocks
}

// handleWalletFeedBlocks serves GET /api/wallet-feed/blocks?from=<height>&limit=<n>.
func (s *APIServer) handleWalletFeedBlocks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err := strconv.ParseUint(q.Get("from"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "from must be an unsigned integer")
		return
	}
	limit := walletFeedDefaultLimit
	if v := q.Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > walletFeedMaxLimit {
			writeError(w, http.StatusBadRequest, "limit must be 1..100")
			return
		}
	}
	tipHeight, tipHash, blocks := s.daemon.Chain().CanonicalRange(from, limit)
	type tip struct {
		Height uint64 `json:"height"`
		Hash   string `json:"hash"`
	}
	page := struct {
		Tip    tip               `json:"tip"`
		Blocks []walletFeedBlock `json:"blocks"`
	}{Tip: tip{tipHeight, hex.EncodeToString(tipHash[:])}, Blocks: make([]walletFeedBlock, 0, len(blocks))}
	for _, b := range blocks {
		hash := b.Hash()
		fb := walletFeedBlock{
			Height:       b.Header.Height,
			Hash:         hex.EncodeToString(hash[:]),
			PrevHash:     hex.EncodeToString(b.Header.PrevHash[:]),
			Timestamp:    b.Header.Timestamp,
			Transactions: make([]walletFeedTx, len(b.Transactions)),
		}
		for i, tx := range b.Transactions {
			ftx, err := walletFeedTxJSON(tx, i == 0 && b.Header.Height > 0)
			if err != nil {
				writeInternal(w, r, http.StatusInternalServerError, "unable to encode block", err)
				return
			}
			fb.Transactions[i] = ftx
		}
		page.Blocks = append(page.Blocks, fb)
	}
	writeJSON(w, http.StatusOK, page)
}

type walletFeedTxStatus struct {
	Status    string        `json:"status"`
	Height    uint64        `json:"height,omitempty"`
	BlockHash string        `json:"block_hash,omitempty"`
	Tx        *walletFeedTx `json:"tx,omitempty"`
}

// handleWalletFeedTx serves GET /api/wallet-feed/tx/{txid}.
func (s *APIServer) handleWalletFeedTx(w http.ResponseWriter, r *http.Request) {
	raw, err := hex.DecodeString(r.PathValue("txid"))
	if err != nil || len(raw) != 32 {
		writeError(w, http.StatusBadRequest, "txid must be 64 hex characters")
		return
	}
	var txID [32]byte
	copy(txID[:], raw)
	if tx, ok := s.daemon.Mempool().GetTransaction(txID); ok {
		ftx, err := walletFeedTxJSON(tx, false)
		if err != nil {
			writeInternal(w, r, http.StatusInternalServerError, "unable to encode transaction", err)
			return
		}
		writeJSON(w, http.StatusOK, walletFeedTxStatus{Status: "mempool", Tx: &ftx})
		return
	}
	chain := s.daemon.Chain()
	tx, height, err := chain.Storage().FindCanonicalTransaction(txID)
	if err != nil {
		writeInternal(w, r, http.StatusInternalServerError, "unable to read transaction", err)
		return
	}
	var block *Block
	if tx != nil {
		block = chain.GetBlockByHeight(height)
	}
	if block == nil {
		writeJSON(w, http.StatusOK, walletFeedTxStatus{Status: "unknown"})
		return
	}
	isCoinbase := false
	for i, btx := range block.Transactions {
		if id, _ := btx.TxID(); id == txID {
			isCoinbase = i == 0 && height > 0
			break
		}
	}
	ftx, err := walletFeedTxJSON(tx, isCoinbase)
	if err != nil {
		writeInternal(w, r, http.StatusInternalServerError, "unable to encode transaction", err)
		return
	}
	hash := block.Hash()
	writeJSON(w, http.StatusOK, walletFeedTxStatus{Status: "canonical", Height: height, BlockHash: hex.EncodeToString(hash[:]), Tx: &ftx})
}
