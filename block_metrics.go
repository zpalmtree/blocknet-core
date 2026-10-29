package main

import (
	"fmt"
	"time"
)

type validationStageReporter func(string)

func (r validationStageReporter) Set(stage string) {
	if r != nil {
		r(stage)
	}
}

type validationTimingBreakdown struct {
	SpentContext      time.Duration
	Checkpoint        time.Duration
	ParentContext     time.Duration
	Difficulty        time.Duration
	TimestampRules    time.Duration
	PoW               time.Duration
	BlockShape        time.Duration
	TxValidation      time.Duration
	DuplicateKeyImage time.Duration
	CoinbaseConsensus time.Duration
	MerkleRoot        time.Duration
	SkipPoW           bool
	TxCount           int
	NonCoinbaseTxs    int
	InputCount        int
	OutputCount       int
}

func (t validationTimingBreakdown) Summary() string {
	powValue := t.PoW.String()
	if t.SkipPoW {
		powValue = "skipped"
	}

	return fmt.Sprintf(
		"txs=%d non_coinbase=%d inputs=%d outputs=%d skip_pow=%t spent_ctx=%s checkpoint=%s parent_ctx=%s difficulty=%s timestamp=%s pow=%s block_shape=%s tx_validation=%s dup_key_images=%s coinbase_consensus=%s merkle_root=%s",
		t.TxCount,
		t.NonCoinbaseTxs,
		t.InputCount,
		t.OutputCount,
		t.SkipPoW,
		t.SpentContext,
		t.Checkpoint,
		t.ParentContext,
		t.Difficulty,
		t.TimestampRules,
		powValue,
		t.BlockShape,
		t.TxValidation,
		t.DuplicateKeyImage,
		t.CoinbaseConsensus,
		t.MerkleRoot,
	)
}

type chainTipSnapshot struct {
	Height         uint64
	BestHash       [32]byte
	TotalWork      uint64
	NextDifficulty uint64
}

type processBlockSnapshot struct {
	Active                          bool
	CurrentHeight                   uint64
	CurrentTxCount                  int
	CurrentStage                    string
	CurrentStartedAtUnixMillis      int64
	CurrentStageStartedAtUnixMillis int64
	LastHeight                      uint64
	LastTxCount                     int
	LastCompletedAtUnixMillis       int64
	LastValidateMillis              uint64
	LastCommitMillis                uint64
	LastReorgMillis                 uint64
	LastTotalMillis                 uint64
	LastAccepted                    bool
	LastMainChain                   bool
	LastError                       string
}

func (c *Chain) currentTipSnapshotLocked() chainTipSnapshot {
	return chainTipSnapshot{
		Height:         c.height,
		BestHash:       c.bestHash,
		TotalWork:      c.totalWork,
		NextDifficulty: c.nextDifficultyLocked(),
	}
}

func (c *Chain) updateTipSnapshotLocked() {
	c.publishTipLocked()
}

func (c *Chain) TipSnapshot() chainTipSnapshot {
	if snapshot := c.tipSnap.Load(); snapshot != nil {
		return chainTipSnapshot{Height: snapshot.height, BestHash: snapshot.bestHash, TotalWork: snapshot.totalWork, NextDifficulty: snapshot.nextDifficulty}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentTipSnapshotLocked()
}

func (c *Chain) currentProcessBlockSnapshot() processBlockSnapshot {
	if snapshot, ok := c.processBlockSnapshot.Load().(processBlockSnapshot); ok {
		return snapshot
	}
	return processBlockSnapshot{}
}

func (c *Chain) updateProcessBlockSnapshot(mutator func(*processBlockSnapshot)) {
	snapshot := c.currentProcessBlockSnapshot()
	mutator(&snapshot)
	c.processBlockSnapshot.Store(snapshot)
}

func (c *Chain) ProcessBlockSnapshot() processBlockSnapshot {
	return c.currentProcessBlockSnapshot()
}

func (c *Chain) setProcessBlockStage(
	block *Block,
	currentStage *string,
	stageStartedAt *time.Time,
	stage string,
) {
	*currentStage = stage
	*stageStartedAt = time.Now()
	if block == nil {
		return
	}

	c.updateProcessBlockSnapshot(func(snapshot *processBlockSnapshot) {
		snapshot.CurrentStage = stage
		snapshot.CurrentStageStartedAtUnixMillis = stageStartedAt.UnixMilli()
	})
}

// processBlockTimings is the stage breakdown of one ProcessBlock call.
type processBlockTimings struct {
	Preverify time.Duration
	Validate  time.Duration
	Reorg     time.Duration
	Commit    time.Duration
	Total     time.Duration
	Detail    validationTimingBreakdown
}
