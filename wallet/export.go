package wallet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ExportEncrypted returns a native wallet backup encrypted with the user's
// chosen backup password, never the hosting service's storage key.
func (w *Wallet) ExportEncrypted(password []byte) ([]byte, error) {
	if len(password) < 12 {
		return nil, fmt.Errorf("backup password must be at least 12 bytes")
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	data := w.data
	if !data.ViewOnly && data.Mnemonic == "" {
		mnemonic, err := w.readMnemonicFromDisk()
		if err != nil {
			return nil, err
		}
		data.Mnemonic = mnemonic
	}
	plain, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	defer wipeBytes(plain)
	return encrypt(plain, password)
}

// Replace a complete file instead of truncating the live wallet. On Unix, sync
// the containing directory too so the rename survives a machine crash.
func writeWalletAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".wallet-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
