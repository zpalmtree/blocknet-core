package wallet

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveCachesOnlyEncryptedMnemonicAndPreservesHistory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := testWalletConfigNoCryptoDeps(t)
	w, err := NewWallet(filepath.Join(t.TempDir(), "wallet.dat"), []byte("pw"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	phrase, err := w.Mnemonic()
	if err != nil {
		t.Fatal(err)
	}
	if len(w.encryptedMnemonic) == 0 || bytes.Contains(w.encryptedMnemonic, []byte(phrase)) || w.data.Mnemonic != "" {
		t.Fatal("mnemonic cache must contain only ciphertext")
	}
	w.AddOutput(&OwnedOutput{TxID: [32]byte{1}, OneTimePubKey: [32]byte{2}, Amount: 123, BlockHeight: 10, Spent: true, SpentHeight: 20})
	w.RecordSend(&SendRecord{TxID: [32]byte{3}, Fee: 12, Timestamp: 42})
	if err := w.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(w.filename)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decrypt(raw, w.password)
	if err != nil {
		t.Fatal(err)
	}
	defer wipeBytes(plain)
	if bytes.Contains(plain, []byte("\n  ")) {
		t.Fatal("wallet should use compact JSON")
	}
	loaded, err := LoadWallet(w.filename, []byte("pw"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.AllOutputs()) != 1 || !loaded.AllOutputs()[0].Spent || len(loaded.SendRecords()) != 1 {
		t.Fatal("save lost transaction history")
	}
	got, err := loaded.Mnemonic()
	if err != nil || got != phrase {
		t.Fatalf("recovery phrase changed: %v", err)
	}
}

func TestSaveFailsClosedWhenExistingMnemonicCannotBeRead(t *testing.T) {
	file := filepath.Join(t.TempDir(), "wallet.dat")
	original := []byte("damaged ciphertext")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	w := &Wallet{filename: file, password: []byte("pw")}
	if err := w.Save(); err == nil {
		t.Fatal("must not overwrite unreadable recovery phrase")
	}
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("existing file was overwritten")
	}
}

func BenchmarkWalletSerialization(b *testing.B) {
	data := WalletData{Version: 1}
	for i := 0; i < 10000; i++ {
		data.Outputs = append(data.Outputs, &OwnedOutput{TxID: [32]byte{byte(i)}, Amount: 123456789, BlockHeight: uint64(i), Spent: i%10 != 0})
	}
	for _, compact := range []bool{false, true} {
		name := "indented"
		if compact {
			name = "compact"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var raw []byte
				var err error
				if compact {
					raw, err = json.Marshal(data)
				} else {
					raw, err = json.MarshalIndent(data, "", "  ")
				}
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(len(raw)), "bytes/file")
			}
		})
	}
}

func TestSaveSerializesConcurrentSnapshotsWithoutLosingOutputs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := testWalletConfigNoCryptoDeps(t)
	w, err := NewWallet(filepath.Join(t.TempDir(), "wallet.dat"), []byte("pw"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		var key [32]byte
		key[0], key[1] = byte(i), byte(i>>8)
		w.AddOutput(&OwnedOutput{TxID: key, OneTimePubKey: key, Amount: 1000, BlockHeight: 10})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			var key [32]byte
			key[0], key[1] = byte(i), byte(i>>8)
			w.MarkSpentByTx(key, [32]byte{42})
		}
	}()
	results := make(chan error, 2)
	go func() { results <- w.Save() }()
	go func() { results <- w.Save() }()
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if err := w.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWallet(w.filename, []byte("pw"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	outputs := loaded.AllOutputs()
	if len(outputs) != 1000 {
		t.Fatalf("lost outputs: %d", len(outputs))
	}
	for _, output := range outputs {
		if !output.Spent {
			t.Fatal("lost concurrent spent state")
		}
	}
}
