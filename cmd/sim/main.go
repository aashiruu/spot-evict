package main

import (
"encoding/csv"
"encoding/json"
"flag"
"fmt"
"os"
"strconv"

"github.com/aashiruu/spot-evict/internal/sim"
)

type SweepReport struct {
Pricing             sim.PricingParams `json:"pricing"`
BaseComputeSec      float64           `json:"base_compute_sec"`
RestartOverheadSec  float64           `json:"restart_overhead_sec"`
CheckpointWriteSec  float64           `json:"checkpoint_write_sec"`
GracefulNoticeProb  float64           `json:"graceful_notice_prob"`
BreakEvenEvictPerHr float64           `json:"break_even_evictions_per_hr"`
Results             []sim.CellResult  `json:"results"`
}

func main() {
baseSec := flag.Float64("base-sec", 7200, "Uninterrupted compute duration in seconds (default 7200 = 2 hours)")
trials := flag.Int("trials", 500, "Monte Carlo trials per grid cell")
jsonPath := flag.String("json-out", "results/stage7-8-sim-cost.json", "Output JSON path")
csvPath := flag.String("csv-out", "results/stage7-8-sim-cost.csv", "Output CSV path")
flag.Parse()

price := sim.DefaultUSEast1Pricing()
evictRates := []float64{0.0, 0.25, 0.5, 1.0, 2.0, 4.0, 8.0}
ckptIntervals := []float64{10, 30, 60, 120, 300, 600, 1800, 0}

var results []sim.CellResult
for idx, rate := range evictRates {
for jdx, ckpt := range ckptIntervals {
spec := sim.JobSpec{
BaseComputeSec:       *baseSec,
RestartOverheadSec:   13.64,
CheckpointWriteSec:   0.06,
HeartbeatIntervalSec: 5.0,
GracefulNoticeProb:   0.95,
EvictionsPerHour:     rate,
CheckpointEverySec:   ckpt,
Trials:               *trials,
Seed:                 int64(1000 + idx*100 + jdx),
}
results = append(results, sim.RunCell(spec, price))
}
}

// Find approximate break-even eviction rate (at 300s periodic checkpointing) where SpotTotalUSD >= OnDemandTotalUSD
breakEven := 0.0
for r := 1.0; r <= 300.0; r += 1.0 {
c := sim.RunCell(sim.JobSpec{
BaseComputeSec:       *baseSec,
RestartOverheadSec:   13.64,
CheckpointWriteSec:   0.06,
HeartbeatIntervalSec: 5.0,
GracefulNoticeProb:   0.95,
EvictionsPerHour:     r,
CheckpointEverySec:   300,
Trials:               200,
Seed:                 999,
}, price)
if c.SpotTotalUSD >= c.OnDemandTotalUSD {
breakEven = r
break
}
}

report := SweepReport{
Pricing:             price,
BaseComputeSec:      *baseSec,
RestartOverheadSec:  13.64,
CheckpointWriteSec:  0.06,
GracefulNoticeProb:  0.95,
BreakEvenEvictPerHr: breakEven,
Results:             results,
}

rawJSON, _ := json.MarshalIndent(report, "", "  ")
_ = os.WriteFile(*jsonPath, rawJSON, 0o644)

f, err := os.Create(*csvPath)
if err != nil {
fmt.Fprintf(os.Stderr, "csv create error: %v\n", err)
os.Exit(1)
}
defer f.Close()
w := csv.NewWriter(f)
_ = w.Write([]string{
"evictions_per_hour", "checkpoint_every_sec", "mean_wall_sec", "p95_wall_sec",
"mean_evictions", "wasted_compute_pct", "checkpoint_write_pct",
"s3_put_calls", "ddb_write_units", "on_demand_total_usd", "spot_total_usd", "savings_pct",
})
for _, r := range results {
_ = w.Write([]string{
fmt.Sprintf("%.2f", r.EvictionsPerHour),
fmt.Sprintf("%.0f", r.CheckpointEverySec),
fmt.Sprintf("%.2f", r.MeanWallSec),
fmt.Sprintf("%.2f", r.P95WallSec),
fmt.Sprintf("%.2f", r.MeanEvictions),
fmt.Sprintf("%.2f", r.WastedComputePct),
fmt.Sprintf("%.4f", r.CheckpointWritePct),
fmt.Sprintf("%.2f", r.S3PutCalls),
fmt.Sprintf("%.2f", r.DDBWriteUnits),
strconv.FormatFloat(r.OnDemandTotalUSD, 'f', 6, 64),
strconv.FormatFloat(r.SpotTotalUSD, 'f', 6, 64),
fmt.Sprintf("%.2f", r.SavingsPct),
})
}
w.Flush()

fmt.Printf("Simulated %d cells (base=%.0fs, trials=%d) -> %s & %s | Break-even eviction rate: %.0f evictions/hr\n",
len(results), *baseSec, *trials, *jsonPath, *csvPath, breakEven)
}
