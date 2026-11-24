package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aashiruu/spot-evict/internal/workload"
)

const maxAllowedChunks = 100000 // Hard cap to prevent runaway billing

type RunResult struct {
	RunID        string  `json:"run_id"`
	InstanceID   string  `json:"instance_id,omitempty"`
	InstanceType string  `json:"instance_type,omitempty"`
	StartedAt    string  `json:"started_at"`
	FinishedAt   string  `json:"finished_at"`
	ElapsedSec   float64 `json:"elapsed_seconds"`
	Chunks       int64   `json:"chunks"`
	Rounds       int     `json:"rounds"`
	MsPerChunk   float64 `json:"ms_per_chunk"`
	ChunksPerSec float64 `json:"chunks_per_sec"`
	Checksum     string  `json:"checksum"`
}

func main() {
	chunks := flag.Int64("chunks", 100, "Number of fixed chunks to process")
	rounds := flag.Int("rounds", 10000, "SHA-256 rounds per chunk")
	runID := flag.String("run-id", fmt.Sprintf("run-%d", time.Now().UTC().Unix()), "Unique run identifier")
	jsonOut := flag.Bool("json", false, "Emit structured JSON output")
	flag.Parse()

	if *chunks <= 0 || *chunks > maxAllowedChunks {
		fmt.Fprintf(os.Stderr, "error: chunks must be between 1 and %d\n", maxAllowedChunks)
		os.Exit(1)
	}

	started := time.Now().UTC()
	state := workload.NewState(*chunks, *rounds)
	elapsed, err := workload.Run(&state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker error: %v\n", err)
		os.Exit(1)
	}
	finished := time.Now().UTC()

	msPerChunk := float64(elapsed.Microseconds()) / 1000.0 / float64(state.ChunksProcessed)
	chunksPerSec := float64(state.ChunksProcessed) / elapsed.Seconds()

	if *jsonOut {
		res := RunResult{
			RunID:        *runID,
			InstanceID:   os.Getenv("INSTANCE_ID"),
			InstanceType: os.Getenv("INSTANCE_TYPE"),
			StartedAt:    started.Format(time.RFC3339Nano),
			FinishedAt:   finished.Format(time.RFC3339Nano),
			ElapsedSec:   elapsed.Seconds(),
			Chunks:       state.ChunksProcessed,
			Rounds:       state.RoundsPerChunk,
			MsPerChunk:   msPerChunk,
			ChunksPerSec: chunksPerSec,
			Checksum:     state.RunningHashHex,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintf(os.Stderr, "json encode error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("run_id=%s chunks=%d rounds=%d elapsed=%s ms_per_chunk=%.3f chunks_per_sec=%.2f checksum=%s\n",
		*runID,
		state.ChunksProcessed,
		state.RoundsPerChunk,
		elapsed.Round(time.Millisecond),
		msPerChunk,
		chunksPerSec,
		state.RunningHashHex,
	)
}
