package workload

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

// State holds the checkpointable progress of a verifiable batch job.
type State struct {
	NextChunk       int64  `json:"next_chunk"`
	TotalChunks     int64  `json:"total_chunks"`
	RoundsPerChunk  int    `json:"rounds_per_chunk"`
	RunningHashHex  string `json:"running_hash_hex"`
	ChunksProcessed int64  `json:"chunks_processed"`
}

// NewState initializes a fresh job state with a fixed seed hash.
func NewState(totalChunks int64, roundsPerChunk int) State {
	seed := sha256.Sum256([]byte("spot-evict-seed-v1"))
	return State{
		NextChunk:       0,
		TotalChunks:     totalChunks,
		RoundsPerChunk:  roundsPerChunk,
		RunningHashHex:  hex.EncodeToString(seed[:]),
		ChunksProcessed: 0,
	}
}

// ComputeChunk processes one fixed chunk and folds it into the running SHA-256 chain.
func ComputeChunk(state *State) error {
	if state.NextChunk >= state.TotalChunks {
		return fmt.Errorf("chunk %d out of bounds (total %d)", state.NextChunk, state.TotalChunks)
	}

	prevBytes, err := hex.DecodeString(state.RunningHashHex)
	if err != nil || len(prevBytes) != sha256.Size {
		return fmt.Errorf("invalid running hash: %w", err)
	}

	// Build a fixed 64 KiB block derived from chunk index and previous digest.
	const blockSize = 64 * 1024
	buf := make([]byte, blockSize)
	binary.BigEndian.PutUint64(buf[0:8], uint64(state.NextChunk))
	copy(buf[8:40], prevBytes)
	for i := 40; i < blockSize; i++ {
		buf[i] = byte((int(state.NextChunk) + i) & 0xff)
	}

	// Perform fixed CPU work per chunk.
	digest := sha256.Sum256(buf)
	for r := 1; r < state.RoundsPerChunk; r++ {
		digest = sha256.Sum256(digest[:])
	}

	// Fold chunk index, previous running hash, and chunk digest into the new running hash.
	chainHasher := sha256.New()
	var idxBuf [8]byte
	binary.BigEndian.PutUint64(idxBuf[:], uint64(state.NextChunk))
	chainHasher.Write(idxBuf[:])
	chainHasher.Write(prevBytes)
	chainHasher.Write(digest[:])
	nextHash := chainHasher.Sum(nil)

	state.RunningHashHex = hex.EncodeToString(nextHash)
	state.NextChunk++
	state.ChunksProcessed++
	return nil
}

// Run executes all remaining chunks in state and returns elapsed duration.
func Run(state *State) (time.Duration, error) {
	start := time.Now()
	for state.NextChunk < state.TotalChunks {
		if err := ComputeChunk(state); err != nil {
			return time.Since(start), err
		}
	}
	return time.Since(start), nil
}
