package wallet

import (
	"encoding/json"
	"fmt"
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
