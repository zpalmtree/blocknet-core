package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"blocknet/p2p"
	"blocknet/wallet"
)

func TestHandleBlockTemplate_CompactOmitsTransactionsAndServesThemByTemplateID(t *testing.T) {
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()
	mustAddGenesisBlock(t, chain)

	daemon, stopDaemon := mustStartTestDaemon(t, chain)
	defer stopDaemon()
	daemon.syncMgr = new(p2p.SyncManager) // avoid nil deref in handler

	w, err := wallet.NewWallet(filepath.Join(t.TempDir(), "wallet.dat"), []byte("pw"), defaultWalletConfig())
	if err != nil {
		t.Fatalf("failed to create wallet: %v", err)
	}
	api := NewAPIServer(daemon, w, nil, t.TempDir(), []byte("pw"))
	mux := http.NewServeMux()
	api.registerPublicRoutes(mux)
	api.registerPrivateRoutes(mux)
	token := "test-token"
	handler := authMiddleware(token, mux)

	doReq := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "198.51.100.30:1234"
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	rr := doReq("/api/mining/blocktemplate?compact=1")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var compact struct {
		Block      map[string]json.RawMessage `json:"block"`
		Compact    bool                       `json:"compact"`
		TxCount    int                        `json:"tx_count"`
		TemplateID string                     `json:"template_id"`
		HeaderBase string                     `json:"header_base"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &compact); err != nil {
		t.Fatalf("failed to decode compact template: %v", err)
	}
	if !compact.Compact || compact.TxCount < 1 || compact.TemplateID == "" || compact.HeaderBase == "" {
		t.Fatalf("unexpected compact template: %+v", compact)
	}
	if _, ok := compact.Block["header"]; !ok || len(compact.Block) != 1 {
		t.Fatalf("compact block should carry only its header, got keys %v", compact.Block)
	}

	rr = doReq("/api/mining/templateblock?template_id=" + compact.TemplateID)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for templateblock, got %d: %s", rr.Code, rr.Body.String())
	}
	var full Block
	if err := json.Unmarshal(rr.Body.Bytes(), &full); err != nil {
		t.Fatalf("failed to decode template block: %v", err)
	}
	if len(full.Transactions) != compact.TxCount {
		t.Fatalf("template block has %d transactions, compact template said %d", len(full.Transactions), compact.TxCount)
	}
	var header BlockHeader
	if err := json.Unmarshal(compact.Block["header"], &header); err != nil {
		t.Fatalf("failed to decode compact header: %v", err)
	}
	if header != full.Header {
		t.Fatalf("compact header differs from the cached block header")
	}

	if rr := doReq("/api/mining/templateblock?template_id=0000000000000000"); rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown template_id, got %d", rr.Code)
	}

	// Without compact the full block is returned as before.
	rr = doReq("/api/mining/blocktemplate")
	var legacy struct {
		Block   *Block `json:"block"`
		Compact bool   `json:"compact"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &legacy); err != nil {
		t.Fatalf("failed to decode full template: %v", err)
	}
	if legacy.Compact || legacy.Block == nil || len(legacy.Block.Transactions) == 0 {
		t.Fatalf("full template should carry transactions")
	}
}
