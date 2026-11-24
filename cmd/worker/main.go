package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/aashiruu/spot-evict/internal/checkpoint"
	"github.com/aashiruu/spot-evict/internal/workload"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const maxAllowedChunks = 100000 // Hard cap to prevent runaway billing

type RunResult struct {
	RunID              string   `json:"run_id"`
	InstanceID         string   `json:"instance_id,omitempty"`
	InstanceType       string   `json:"instance_type,omitempty"`
	StartedAt          string   `json:"started_at"`
	FinishedAt         string   `json:"finished_at"`
	ElapsedSec         float64  `json:"elapsed_seconds"`
	Chunks             int64    `json:"chunks"`
	Rounds             int      `json:"rounds"`
	ResumedFromChunk   int64    `json:"resumed_from_chunk"`
	CheckpointsWritten int      `json:"checkpoints_written"`
	CheckpointBytes    int      `json:"checkpoint_bytes"`
	CheckpointWarnings []string `json:"checkpoint_warnings,omitempty"`
	MsPerChunk         float64  `json:"ms_per_chunk"`
	ChunksPerSec       float64  `json:"chunks_per_sec"`
	Checksum           string   `json:"checksum"`
}

type BenchResult struct {
	RunID         string  `json:"run_id"`
	Samples       int     `json:"samples"`
	PadBytes      int     `json:"pad_bytes"`
	PayloadBytes  int     `json:"payload_bytes"`
	MinWriteMs    float64 `json:"min_write_ms"`
	MedianWriteMs float64 `json:"median_write_ms"`
	MaxWriteMs    float64 `json:"max_write_ms"`
	S3PutCalls    int     `json:"s3_put_calls"`
	S3DeleteCalls int     `json:"s3_delete_calls"`
}

func main() {
	chunks := flag.Int64("chunks", 100, "Number of fixed chunks to process")
	rounds := flag.Int("rounds", 10000, "SHA-256 rounds per chunk")
	runID := flag.String("run-id", fmt.Sprintf("run-%d", time.Now().UTC().Unix()), "Unique run identifier")
	s3Bucket := flag.String("s3-bucket", "", "S3 bucket for checkpoints (empty disables S3)")
	region := flag.String("region", "us-east-1", "AWS region")
	ckptEvery := flag.Int64("checkpoint-every", 0, "Write checkpoint every N chunks (0 disables periodic)")
	padBytes := flag.Int("pad-bytes", 0, "Optional deterministic state padding bytes in checkpoint")
	keepLast := flag.Int("keep-last", 3, "Number of recent checkpoints to retain")
	benchCount := flag.Int("bench-checkpoints", 0, "If >0, benchmark N checkpoint writes to S3 and exit")
	jsonOut := flag.Bool("json", false, "Emit structured JSON output")
	flag.Parse()

	if *chunks <= 0 || *chunks > maxAllowedChunks {
		fmt.Fprintf(os.Stderr, "error: chunks must be between 1 and %d\n", maxAllowedChunks)
		os.Exit(1)
	}

	ctx := context.Background()
	var store *checkpoint.S3Store
	if *s3Bucket != "" {
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(*region))
		if err != nil {
			fmt.Fprintf(os.Stderr, "aws config error: %v\n", err)
			os.Exit(1)
		}
		store = &checkpoint.S3Store{
			Client: s3.NewFromConfig(cfg),
			Bucket: *s3Bucket,
		}
	}

	workerID := os.Getenv("INSTANCE_ID")
	if workerID == "" {
		workerID = "local-worker"
	}

	if *benchCount > 0 {
		if store == nil {
			fmt.Fprintln(os.Stderr, "error: -bench-checkpoints requires -s3-bucket")
			os.Exit(1)
		}
		runCheckpointBenchmark(ctx, store, *runID, workerID, *benchCount, *padBytes, *keepLast)
		return
	}

	started := time.Now().UTC()
	state := workload.NewState(*chunks, *rounds)
	var resumedFrom int64
	var warnings []string

	if store != nil {
		env, w, err := store.LoadLatest(ctx, *runID)
		warnings = append(warnings, w...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "checkpoint load error: %v\n", err)
			os.Exit(1)
		}
		if env != nil {
			state = env.State
			resumedFrom = state.NextChunk
			fmt.Fprintf(os.Stderr, "resumed run=%s from chunk=%d (warnings=%d)\n", *runID, resumedFrom, len(warnings))
		}
	}

	ckptsWritten := 0
	lastBytes := 0
	for state.NextChunk < state.TotalChunks {
		if err := workload.ComputeChunk(&state); err != nil {
			fmt.Fprintf(os.Stderr, "worker error: %v\n", err)
			os.Exit(1)
		}
		if store != nil && *ckptEvery > 0 && state.NextChunk%*ckptEvery == 0 && state.NextChunk < state.TotalChunks {
			m, err := store.Save(ctx, *runID, 1, workerID, state, *padBytes, *keepLast)
			if err != nil {
				fmt.Fprintf(os.Stderr, "checkpoint save error: %v\n", err)
				os.Exit(1)
			}
			ckptsWritten++
			lastBytes = m.Bytes
		}
	}

	if store != nil && *ckptEvery > 0 {
		m, err := store.Save(ctx, *runID, 1, workerID, state, *padBytes, *keepLast)
		if err != nil {
			fmt.Fprintf(os.Stderr, "final checkpoint save error: %v\n", err)
			os.Exit(1)
		}
		ckptsWritten++
		lastBytes = m.Bytes
	}

	finished := time.Now().UTC()
	elapsed := finished.Sub(started)
	processedNow := state.NextChunk - resumedFrom
	var msPerChunk, chunksPerSec float64
	if processedNow > 0 {
		msPerChunk = float64(elapsed.Microseconds()) / 1000.0 / float64(processedNow)
		chunksPerSec = float64(processedNow) / elapsed.Seconds()
	}

	if *jsonOut {
		res := RunResult{
			RunID:              *runID,
			InstanceID:         os.Getenv("INSTANCE_ID"),
			InstanceType:       os.Getenv("INSTANCE_TYPE"),
			StartedAt:          started.Format(time.RFC3339Nano),
			FinishedAt:         finished.Format(time.RFC3339Nano),
			ElapsedSec:         elapsed.Seconds(),
			Chunks:             state.ChunksProcessed,
			Rounds:             state.RoundsPerChunk,
			ResumedFromChunk:   resumedFrom,
			CheckpointsWritten: ckptsWritten,
			CheckpointBytes:    lastBytes,
			CheckpointWarnings: warnings,
			MsPerChunk:         msPerChunk,
			ChunksPerSec:       chunksPerSec,
			Checksum:           state.RunningHashHex,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		return
	}

	fmt.Printf("run_id=%s chunks=%d resumed_from=%d ckpts=%d elapsed=%s checksum=%s\n",
		*runID, state.ChunksProcessed, resumedFrom, ckptsWritten, elapsed.Round(time.Millisecond), state.RunningHashHex)
}

func runCheckpointBenchmark(ctx context.Context, store *checkpoint.S3Store, runID, workerID string, n, padBytes, keepLast int) {
	state := workload.NewState(int64(n+1), 10)
	durationsMs := make([]float64, 0, n)
	var payloadBytes, putCalls, delCalls int
	for i := 0; i < n; i++ {
		_ = workload.ComputeChunk(&state)
		m, err := store.Save(ctx, runID, 1, workerID, state, padBytes, keepLast)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench save failed at %d: %v\n", i, err)
			os.Exit(1)
		}
		durationsMs = append(durationsMs, float64(m.Duration.Microseconds())/1000.0)
		payloadBytes = m.Bytes
		putCalls += m.S3PutCalls
		delCalls += m.S3DelCalls
	}
	sort.Float64s(durationsMs)
	res := BenchResult{
		RunID:         runID,
		Samples:       n,
		PadBytes:      padBytes,
		PayloadBytes:  payloadBytes,
		MinWriteMs:    durationsMs[0],
		MedianWriteMs: durationsMs[len(durationsMs)/2],
		MaxWriteMs:    durationsMs[len(durationsMs)-1],
		S3PutCalls:    putCalls,
		S3DeleteCalls: delCalls,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
}
