package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aashiruu/spot-evict/internal/workload"
)

const maxAllowedChunks = 100000 // Hard cap to prevent runaway billing

func main() {
	chunks := flag.Int64("chunks", 100, "Number of fixed chunks to process")
	rounds := flag.Int("rounds", 10000, "SHA-256 rounds per chunk")
	flag.Parse()

	if *chunks <= 0 || *chunks > maxAllowedChunks {
		fmt.Fprintf(os.Stderr, "error: chunks must be between 1 and %d\n", maxAllowedChunks)
		os.Exit(1)
	}

	state := workload.NewState(*chunks, *rounds)
	elapsed, err := workload.Run(&state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker error: %v\n", err)
		os.Exit(1)
	}

	msPerChunk := float64(elapsed.Microseconds()) / 1000.0 / float64(state.ChunksProcessed)
	chunksPerSec := float64(state.ChunksProcessed) / elapsed.Seconds()

	fmt.Printf("chunks=%d rounds=%d elapsed=%s ms_per_chunk=%.3f chunks_per_sec=%.2f checksum=%s\n",
		state.ChunksProcessed,
		state.RoundsPerChunk,
		elapsed.Round(time.Millisecond),
		msPerChunk,
		chunksPerSec,
		state.RunningHashHex,
	)
}
