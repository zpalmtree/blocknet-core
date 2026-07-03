package main

import (
	"encoding/hex"

	"blocknet/p2p"
)

type PublicChainStatus struct {
	PeerID      string `json:"peer_id,omitempty"`
	ChainHeight uint64 `json:"chain_height"`
	TotalWork   uint64 `json:"total_work"`
	BestHash    string `json:"best_hash"`
	NetworkID   string `json:"network_id"`
	ChainID     uint32 `json:"chain_id"`
}

func buildPublicChainStatus(peerID string, status p2p.ChainStatus) PublicChainStatus {
	return PublicChainStatus{
		PeerID:      peerID,
		ChainHeight: status.Height,
		TotalWork:   status.TotalWork,
		BestHash:    hex.EncodeToString(status.BestHash[:]),
		NetworkID:   status.NetworkID,
		ChainID:     status.ChainID,
	}
}
