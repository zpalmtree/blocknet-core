package main

import (
	"blocknet/wallet"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestWalletOutputsStatusFilter(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()
	mustAddGenesisBlock(t, chain)
	chain.mu.Lock()
	chain.height = 300
	chain.publishTipLocked()
	chain.mu.Unlock()
	daemon, stop := mustStartTestDaemon(t, chain)
	defer stop()
	w, err := wallet.NewWallet(filepath.Join(t.TempDir(), "wallet.dat"), []byte("pw"), defaultWalletConfig())
	if err != nil {
		t.Fatal(err)
	}
	for i, out := range []*wallet.OwnedOutput{
		{Amount: 100, BlockHeight: 200},
		{Amount: 200, BlockHeight: 299},
		{Amount: 300, BlockHeight: 200, Spent: true},
		{Amount: 400, BlockHeight: 250, IsCoinbase: true},
	} {
		out.TxID[0] = byte(i + 1)
		out.OneTimePubKey[0] = byte(i + 1)
		w.AddOutput(out)
	}
	api := NewAPIServer(daemon, w, nil, t.TempDir(), []byte("pw"))
	mux := http.NewServeMux()
	api.registerPrivateRoutes(mux)
	handler := authMiddleware("filter-test", mux)
	for _, tc := range []struct {
		filter      string
		count, code int
	}{{"", 4, 200}, {"unspent", 1, 200}, {"pending", 2, 200}, {"spent", 1, 200}, {"bad", 0, 400}} {
		t.Run(tc.filter, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/wallet/outputs?status="+tc.filter, nil)
			req.Header.Set("Authorization", "Bearer filter-test")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.code {
				t.Fatalf("HTTP %d: %s", rr.Code, rr.Body.String())
			}
			if tc.code != 200 {
				return
			}
			var result struct {
				Total   int
				Outputs []struct{ Status string }
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Total != tc.count || len(result.Outputs) != tc.count {
				t.Fatalf("unexpected output count: %+v", result)
			}
			for _, out := range result.Outputs {
				if tc.filter != "" && out.Status != tc.filter {
					t.Fatal("filter returned wrong state")
				}
			}
		})
	}
}
