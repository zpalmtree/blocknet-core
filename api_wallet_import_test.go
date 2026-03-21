package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"blocknet/wallet"
)

func makeImportServer(t *testing.T, daemon *Daemon, w *wallet.Wallet, password []byte, walletFile string) (http.Handler, *APIServer) {
	t.Helper()
	api := NewAPIServer(daemon, w, nil, t.TempDir(), password)
	api.cli = &CLI{walletFile: walletFile}
	mux := http.NewServeMux()
	api.registerPublicRoutes(mux)
	api.registerPrivateRoutes(mux)
	token := "test-token"
	api.token = token
	var handler http.Handler = mux
	handler = authMiddleware(token, handler)
	handler = maxBodySize(handler, maxRequestBodyBytes)
	return handler, api
}

func doImportReq(t *testing.T, handler http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/wallet/import", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.40:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestHandleImportWallet_AbsoluteFilenameUsesProvidedPath(t *testing.T) {
	chain, _, cleanup := mustCreateTestChain(t)
	defer cleanup()
	mustAddGenesisBlock(t, chain)

	daemon, stopDaemon := mustStartTestDaemon(t, chain)
	defer stopDaemon()

	defaultDir := t.TempDir()
	customDir := t.TempDir()
	handler, _ := makeImportServer(t, daemon, nil, nil, filepath.Join(defaultDir, "default.dat"))

	mnemonic, err := wallet.GenerateMnemonic()
	if err != nil {
		t.Fatalf("generate mnemonic: %v", err)
	}
	customPath := filepath.Join(customDir, "restored.dat")

	body, err := json.Marshal(map[string]string{
		"mnemonic": mnemonic,
		"password": "my-password",
		"filename": customPath,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rr := doImportReq(t, handler, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	if !fileExists(customPath) {
		t.Fatal("expected imported wallet to be written at the provided absolute path")
	}
	if fileExists(filepath.Join(defaultDir, "restored.dat")) {
		t.Fatal("import should not rewrite the target into the configured default wallet directory when an absolute path is provided")
	}
}
