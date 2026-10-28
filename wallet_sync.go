package main

import "blocknet/wallet"

func walletSyncHashKnown(hash [32]byte) bool {
	return hash != ([32]byte{})
}

func rewindWalletToCanonicalTip(w *wallet.Wallet, chain *Chain) int {
	if w == nil || chain == nil {
		return 0
	}

	removed := 0
	for {
		chainHeight := chain.Height()
		walletHeight, walletHash := w.SyncedBlock()
		if walletHeight == 0 {
			return removed
		}
		if walletHeight > chainHeight {
			removed += w.RewindToHeight(chainHeight)
			continue
		}
		if !walletSyncHashKnown(walletHash) {
			return removed
		}

		block := chain.GetBlockByHeight(walletHeight)
		if block == nil {
			removed += w.RewindToHeight(walletHeight - 1)
			continue
		}
		if block.Hash() == walletHash {
			return removed
		}
		removed += w.RewindToHeight(walletHeight - 1)
	}
}

// syncWalletStep moves w one block towards the canonical tip: it scans the
// next block, or rewinds one block when w's tip is not on the canonical chain.
// It returns false when w is already at the tip (or the next block is not
// available). Callers hold CLI.walletScanMu so that the live block loop and a
// catch-up scan advance one shared cursor; each step re-reads w's tip.
func syncWalletStep(chain *Chain, mempool *Mempool, w *wallet.Wallet, sc *wallet.Scanner) bool {
	walletHeight, walletHash := w.SyncedBlock()
	if walletHeight >= chain.Height() {
		return false
	}
	next := chain.GetBlockByHeight(walletHeight + 1)
	if next == nil {
		return false
	}
	if walletSyncHashKnown(walletHash) && next.Header.PrevHash != walletHash {
		if walletHeight == 0 {
			return false
		}
		w.RewindToHeight(walletHeight - 1)
		return true
	}
	data := blockToScanData(next)
	sc.ScanBlock(data)
	if mempool != nil {
		w.ReconcileUnconfirmedSpends(mempool.HasTransaction)
	}
	w.SetSyncedBlock(data.Height, data.Hash)
	return true
}
