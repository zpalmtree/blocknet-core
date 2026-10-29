package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBlockHashBatchEndpoint(t *testing.T) {
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()
	mustAddGenesisBlock(t, chain)
	daemon, stop := mustStartTestDaemon(t, chain)
	defer stop()
	api := &APIServer{daemon: daemon}
	for _, query := range []string{"", "-1", "x", "18446744073709551616", strings.Repeat("0,", 1024) + "0"} {
		rr := httptest.NewRecorder()
		api.handleBlockHashes(rr, httptest.NewRequest("GET", "/api/block-hashes?heights="+query, nil))
		if rr.Code != 400 {
			t.Fatalf("query %.30s: status %d", query, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	api.handleBlockHashes(rr, httptest.NewRequest("GET", "/api/block-hashes?heights=0,0,1", nil))
	var response struct {
		ChainHeight uint64            `json:"chain_height"`
		Hashes      map[uint64]string `json:"hashes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 200 || response.ChainHeight != 0 || len(response.Hashes) != 1 || response.Hashes[0] != fmt.Sprintf("%x", chain.BestHash()) {
		t.Fatalf("response: %d %s", rr.Code, rr.Body.String())
	}
}
