package main

import (
	"math/rand"
	"testing"
)

func randomOutputTx(rng *rand.Rand, outputs int) *Transaction {
	tx := &Transaction{Version: 1}
	rng.Read(tx.TxPublicKey[:])
	for i := 0; i < outputs; i++ {
		var out TxOutput
		rng.Read(out.Commitment[:])
		rng.Read(out.PublicKey[:])
		rng.Read(out.EncryptedAmount[:])
		rng.Read(out.EncryptedMemo[:])
		tx.Outputs = append(tx.Outputs, out)
	}
	return tx
}

func TestTrimTransactionsToJSONBudgetDropsTheLowestPriorityTail(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	header := BlockHeader{Version: 1, Height: 70_000, Timestamp: 1_793_000_000, Difficulty: 123_456_789}
	coinbase := randomOutputTx(rng, 1)
	txs := make([]*Transaction, 0, 40)
	for i := 0; i < 40; i++ {
		txs = append(txs, randomOutputTx(rng, 1+i%4))
	}
	encoded := func(kept []*Transaction) int {
		all := append([]*Transaction{coinbase}, kept...)
		return len(marshalBlockJSON(&Block{Header: header, Transactions: all}))
	}
	full := encoded(txs)

	if got := trimTransactionsToJSONBudget(header, coinbase, txs, full); len(got) != len(txs) {
		t.Fatalf("a template within budget lost transactions: %d of %d kept", len(got), len(txs))
	}

	budget := full / 2
	kept := trimTransactionsToJSONBudget(header, coinbase, txs, budget)
	if size := encoded(kept); size > budget {
		t.Fatalf("trimmed template encodes to %d bytes, over the %d budget", size, budget)
	}
	if len(kept) == 0 || len(kept) >= len(txs) {
		t.Fatalf("kept %d of %d transactions", len(kept), len(txs))
	}
	for i, tx := range kept {
		if tx != txs[i] {
			t.Fatalf("transaction %d is not the fee-ordered prefix", i)
		}
	}
	if size := encoded(txs[:len(kept)+1]); size <= budget {
		t.Fatalf("trimmed one transaction too many: %d more would still fit (%d <= %d)", 1, size, budget)
	}

	if got := trimTransactionsToJSONBudget(header, coinbase, txs, 1); len(got) != 0 {
		t.Fatalf("a budget below the coinbase-only block kept %d transactions", len(got))
	}
}

func TestBlockTemplateJSONBudgetFitsThePeerAnnouncementLimit(t *testing.T) {
	if blockTemplateJSONBudget <= 0 || blockTemplateJSONBudget >= legacyBlockAnnouncementLimit {
		t.Fatalf("budget %d must leave headroom under the %d-byte limit older nodes enforce", blockTemplateJSONBudget, legacyBlockAnnouncementLimit)
	}
}
