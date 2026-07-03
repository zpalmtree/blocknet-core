package main

import (
	"testing"

	"blocknet/p2p"
)

func TestBuildPublicChainStatusFormatsFields(t *testing.T) {
	status := p2p.ChainStatus{
		BestHash:  [32]byte{0x12, 0x34, 0xab},
		Height:    42,
		TotalWork: 99,
		NetworkID: "blocknet-mainnet",
		ChainID:   7,
	}

	got := buildPublicChainStatus("peer-123", status)
	if got.PeerID != "peer-123" {
		t.Fatalf("peer id = %q, want peer-123", got.PeerID)
	}
	if got.ChainHeight != 42 {
		t.Fatalf("chain height = %d, want 42", got.ChainHeight)
	}
	if got.TotalWork != 99 {
		t.Fatalf("total work = %d, want 99", got.TotalWork)
	}
	if got.BestHash != "1234ab0000000000000000000000000000000000000000000000000000000000" {
		t.Fatalf("best hash = %q", got.BestHash)
	}
	if got.NetworkID != "blocknet-mainnet" {
		t.Fatalf("network id = %q", got.NetworkID)
	}
	if got.ChainID != 7 {
		t.Fatalf("chain id = %d, want 7", got.ChainID)
	}
}
