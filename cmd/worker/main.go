package main

import (
"context"
"encoding/json"
"errors"
"flag"
"fmt"
"os"
"sort"
"sync/atomic"
"time"

"github.com/aashiruu/spot-evict/internal/checkpoint"
"github.com/aashiruu/spot-evict/internal/eventlog"
"github.com/aashiruu/spot-evict/internal/imds"
"github.com/aashiruu/spot-evict/internal/lease"
"github.com/aashiruu/spot-evict/internal/workload"
"github.com/aws/aws-sdk-go-v2/config"
"github.com/aws/aws-sdk-go-v2/service/dynamodb"
"github.com/aws/aws-sdk-go-v2/service/s3"
)

const maxAllowedChunks = 100000 // Hard cap to prevent runaway billing

type RunResult struct {
RunID              string                   `json:"run_id"`
WorkerID           string                   `json:"worker_id,omitempty"`
InstanceID         string                   `json:"instance_id,omitempty"`
InstanceType       string                   `json:"instance_type,omitempty"`
InstanceLifecycle  string                   `json:"instance_lifecycle,omitempty"`
FencingToken       int64                    `json:"fencing_token"`
StartedAt          string                   `json:"started_at"`
FinishedAt         string                   `json:"finished_at"`
ElapsedSec         float64                  `json:"elapsed_seconds"`
Chunks             int64                    `json:"chunks"`
TargetChunks       int64                    `json:"target_chunks"`
Completed          bool                     `json:"completed"`
Interrupted        bool                     `json:"interrupted"`
FencedOut          bool                     `json:"fenced_out,omitempty"`
InterruptionNotice *imds.InterruptionNotice `json:"interruption_notice,omitempty"`
EmergencyFlushMs   float64                  `json:"emergency_flush_ms,omitempty"`
Rounds             int                      `json:"rounds"`
ResumedFromChunk   int64                    `json:"resumed_from_chunk"`
CheckpointsWritten int                      `json:"checkpoints_written"`
CheckpointBytes    int                      `json:"checkpoint_bytes"`
CheckpointWarnings []string                 `json:"checkpoint_warnings,omitempty"`
MsPerChunk         float64                  `json:"ms_per_chunk"`
ChunksPerSec       float64                  `json:"chunks_per_sec"`
Checksum           string                   `json:"checksum"`
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
workerIDFlag := flag.String("worker-id", "", "Explicit worker ID override (defaults to INSTANCE_ID or local-worker)")
s3Bucket := flag.String("s3-bucket", "", "S3 bucket for checkpoints (empty disables S3)")
leaseTable := flag.String("lease-table", "", "DynamoDB table for worker leases & fencing tokens (empty disables)")
leaseTTLMs := flag.Int("lease-ttl-ms", 15000, "Lease TTL in milliseconds")
region := flag.String("region", "us-east-1", "AWS region")
ckptEvery := flag.Int64("checkpoint-every", 0, "Write checkpoint every N chunks (0 disables periodic)")
padBytes := flag.Int("pad-bytes", 0, "Optional deterministic state padding bytes in checkpoint")
keepLast := flag.Int("keep-last", 3, "Number of recent checkpoints to retain")
watchSpotITN := flag.Bool("watch-spot-itn", false, "Poll EC2 IMDSv2 for 2-minute Spot Instance Interruption Notice")
imdsEndpoint := flag.String("imds-endpoint", "", "Override IMDS base URL (for mocked ITN testing)")
imdsPollMs := flag.Int("imds-poll-ms", 500, "IMDS Spot ITN polling interval in milliseconds")
eventLogPath := flag.String("event-log", "", "Path to write structured JSONL event log")
benchCount := flag.Int("bench-checkpoints", 0, "If >0, benchmark N checkpoint writes to S3 and exit")
jsonOut := flag.Bool("json", false, "Emit structured JSON output")
flag.Parse()

if *chunks <= 0 || *chunks > maxAllowedChunks {
fmt.Fprintf(os.Stderr, "error: chunks must be between 1 and %d\n", maxAllowedChunks)
os.Exit(1)
}

ctx, cancel := context.WithCancel(context.Background())
defer cancel()

var s3Client *s3.Client
var store *checkpoint.S3Store
var leaseMgr lease.Manager
if *s3Bucket != "" || *leaseTable != "" {
cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(*region))
if err != nil {
fmt.Fprintf(os.Stderr, "aws config error: %v\n", err)
os.Exit(1)
}
if *s3Bucket != "" {
s3Client = s3.NewFromConfig(cfg)
store = &checkpoint.S3Store{
Client: s3Client,
Bucket: *s3Bucket,
}
}
if *leaseTable != "" {
leaseMgr = &lease.DynamoManager{
Client: dynamodb.NewFromConfig(cfg),
Table:  *leaseTable,
}
}
}

workerID := *workerIDFlag
if workerID == "" {
workerID = os.Getenv("INSTANCE_ID")
}
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

evLog, err := eventlog.New(*eventLogPath, *runID, workerID, os.Getenv("INSTANCE_ID"), *chunks)
if err != nil {
fmt.Fprintf(os.Stderr, "event log open error: %v\n", err)
os.Exit(1)
}
defer evLog.Close()

var fencingToken int64 = 1
var currentChunk atomic.Int64
evLog.Emit("worker_start", fencingToken, 0, fmt.Sprintf("lifecycle=%s type=%s", os.Getenv("INSTANCE_LIFECYCLE"), os.Getenv("INSTANCE_TYPE")), 0, 0)

leaseTTL := time.Duration(*leaseTTLMs) * time.Millisecond
var fencedOut atomic.Bool

if leaseMgr != nil {
l, err := leaseMgr.Acquire(ctx, *runID, workerID, leaseTTL)
if err != nil {
evLog.Emit("lease_lost", fencingToken, 0, fmt.Sprintf("acquire_failed: %v", err), 0, 0)
_ = evLog.FlushToS3(ctx, s3Client, *s3Bucket)
fmt.Fprintf(os.Stderr, "lease acquire failed (worker=%s run=%s): %v\n", workerID, *runID, err)
os.Exit(2)
}
fencingToken = l.FencingToken
evLog.Emit("lease_acquired", fencingToken, 0, fmt.Sprintf("ttl_ms=%d", *leaseTTLMs), 0, 0)
fmt.Fprintf(os.Stderr, "acquired lease run=%s worker=%s fence=%d ttl=%s\n", *runID, workerID, fencingToken, leaseTTL)

hbInterval := leaseTTL / 3
if hbInterval < 200*time.Millisecond {
hbInterval = 200 * time.Millisecond
}
go func() {
ticker := time.NewTicker(hbInterval)
defer ticker.Stop()
for {
select {
case <-ctx.Done():
return
case <-ticker.C:
if _, err := leaseMgr.VerifyAndRenew(ctx, *runID, workerID, fencingToken, leaseTTL); err != nil {
if errors.Is(err, lease.ErrFencedOut) {
fencedOut.Store(true)
evLog.Emit("lease_lost", fencingToken, currentChunk.Load(), "heartbeat_fenced_out", 0, 0)
fmt.Fprintf(os.Stderr, "HEARTBEAT FENCED OUT: worker=%s fence=%d lost lease!\n", workerID, fencingToken)
return
}
}
}
}
}()
}

watcher := imds.NewWatcher(*imdsEndpoint, time.Duration(*imdsPollMs)*time.Millisecond)
watcher.Start(ctx, *watchSpotITN || *imdsEndpoint != "")

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
currentChunk.Store(resumedFrom)
if leaseMgr == nil {
fencingToken = env.FencingToken + 1
}
evLog.Emit("checkpoint_loaded", fencingToken, resumedFrom, fmt.Sprintf("warnings=%d", len(warnings)), 0, 0)
fmt.Fprintf(os.Stderr, "resumed run=%s from chunk=%d fence=%d (warnings=%d)\n", *runID, resumedFrom, fencingToken, len(warnings))
}
}

saveFenced := func(st workload.State) (checkpoint.WriteMetrics, error) {
if leaseMgr != nil {
if _, err := leaseMgr.VerifyAndRenew(ctx, *runID, workerID, fencingToken, leaseTTL); err != nil {
if errors.Is(err, lease.ErrFencedOut) {
fencedOut.Store(true)
evLog.Emit("lease_lost", fencingToken, st.NextChunk, "pre_write_fenced_out", 0, 0)
}
return checkpoint.WriteMetrics{}, err
}
}
return store.Save(ctx, *runID, fencingToken, workerID, st, *padBytes, *keepLast)
}

ckptsWritten := 0
lastBytes := 0
interrupted := false
var emergencyFlushMs float64

// Emit periodic chunk_done progress markers (every 10% of target chunks) for timeline replay
progressStep := *chunks / 10
if progressStep < 1 {
progressStep = 1
}

for state.NextChunk < state.TotalChunks {
if fencedOut.Load() {
break
}
if watcher.Triggered() {
interrupted = true
n := watcher.Notice()
detail := ""
if n != nil {
detail = fmt.Sprintf("source=%s action=%s", n.Source, n.Action)
}
evLog.Emit("notice_received", fencingToken, state.NextChunk, detail, 0, 0)
break
}
if err := workload.ComputeChunk(&state); err != nil {
fmt.Fprintf(os.Stderr, "worker error: %v\n", err)
os.Exit(1)
}
currentChunk.Store(state.NextChunk)
if state.NextChunk%progressStep == 0 || state.NextChunk == state.TotalChunks {
evLog.Emit("chunk_done", fencingToken, state.NextChunk, state.RunningHashHex[:16], 0, 0)
}
if store != nil && *ckptEvery > 0 && state.NextChunk%*ckptEvery == 0 && state.NextChunk < state.TotalChunks {
m, err := saveFenced(state)
if err != nil {
if errors.Is(err, lease.ErrFencedOut) {
fmt.Fprintf(os.Stderr, "WRITE REJECTED (FENCED OUT): worker=%s fence=%d chunk=%d — aborting stale write!\n", workerID, fencingToken, state.NextChunk)
break
}
fmt.Fprintf(os.Stderr, "checkpoint save error: %v\n", err)
os.Exit(1)
}
ckptsWritten++
lastBytes = m.Bytes
evLog.Emit("checkpoint_written", fencingToken, state.NextChunk, m.Key, m.Bytes, float64(m.Duration.Microseconds())/1000.0)
}
}

if !fencedOut.Load() {
if interrupted {
notice := watcher.Notice()
fmt.Fprintf(os.Stderr, "INTERRUPTION DETECTED (%s: %s) at chunk=%d — flushing emergency checkpoint...\n",
notice.Source, notice.Action, state.NextChunk)
if store != nil {
flushStart := time.Now()
m, err := saveFenced(state)
if err != nil {
if !errors.Is(err, lease.ErrFencedOut) {
fmt.Fprintf(os.Stderr, "emergency checkpoint save error: %v\n", err)
os.Exit(1)
}
} else {
emergencyFlushMs = float64(time.Since(flushStart).Microseconds()) / 1000.0
ckptsWritten++
lastBytes = m.Bytes
evLog.Emit("checkpoint_written", fencingToken, state.NextChunk, "emergency:"+m.Key, m.Bytes, emergencyFlushMs)
fmt.Fprintf(os.Stderr, "emergency checkpoint flushed in %.2f ms (key=%s)\n", emergencyFlushMs, m.Key)
}
}
} else if store != nil && *ckptEvery > 0 && state.NextChunk >= state.TotalChunks {
m, err := saveFenced(state)
if err != nil {
if !errors.Is(err, lease.ErrFencedOut) {
fmt.Fprintf(os.Stderr, "final checkpoint save error: %v\n", err)
os.Exit(1)
}
} else {
ckptsWritten++
lastBytes = m.Bytes
evLog.Emit("checkpoint_written", fencingToken, state.NextChunk, "final:"+m.Key, m.Bytes, float64(m.Duration.Microseconds())/1000.0)
}
}
}

if leaseMgr != nil && !fencedOut.Load() {
_ = leaseMgr.Release(ctx, *runID, workerID, fencingToken)
}

finished := time.Now().UTC()
elapsed := finished.Sub(started)
processedNow := state.NextChunk - resumedFrom
var msPerChunk, chunksPerSec float64
if processedNow > 0 {
msPerChunk = float64(elapsed.Microseconds()) / 1000.0 / float64(processedNow)
chunksPerSec = float64(processedNow) / elapsed.Seconds()
}

completed := state.NextChunk >= state.TotalChunks && !fencedOut.Load()
termReason := "completed"
if fencedOut.Load() {
termReason = "fenced_out"
} else if interrupted {
termReason = "interrupted_notice"
}
evLog.Emit("termination", fencingToken, state.NextChunk, termReason, 0, elapsed.Seconds()*1000.0)
_ = evLog.FlushToS3(ctx, s3Client, *s3Bucket)

if *jsonOut {
res := RunResult{
RunID:              *runID,
WorkerID:           workerID,
InstanceID:         os.Getenv("INSTANCE_ID"),
InstanceType:       os.Getenv("INSTANCE_TYPE"),
InstanceLifecycle:  os.Getenv("INSTANCE_LIFECYCLE"),
FencingToken:       fencingToken,
StartedAt:          started.Format(time.RFC3339Nano),
FinishedAt:         finished.Format(time.RFC3339Nano),
ElapsedSec:         elapsed.Seconds(),
Chunks:             state.ChunksProcessed,
TargetChunks:       state.TotalChunks,
Completed:          completed,
Interrupted:        interrupted,
FencedOut:          fencedOut.Load(),
InterruptionNotice: watcher.Notice(),
EmergencyFlushMs:   emergencyFlushMs,
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
if fencedOut.Load() {
os.Exit(3)
}
return
}

fmt.Printf("run_id=%s worker=%s chunks=%d/%d completed=%v interrupted=%v fenced_out=%v fence=%d resumed_from=%d ckpts=%d elapsed=%s checksum=%s\n",
*runID, workerID, state.ChunksProcessed, state.TotalChunks, completed, interrupted, fencedOut.Load(), fencingToken, resumedFrom, ckptsWritten, elapsed.Round(time.Millisecond), state.RunningHashHex)
if fencedOut.Load() {
os.Exit(3)
}
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
