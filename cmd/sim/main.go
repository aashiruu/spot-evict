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

type RateComparison struct {
Label                  string  `json:"label"`
Regime                 string  `json:"regime"`
EvictionsPerHour       float64 `json:"evictions_per_hour"`
MTBISec                float64 `json:"mtbi_sec"`
YoungDalySec           float64 `json:"young_daly_sec"`
EmpiricalBestWallSec   float64 `json:"empirical_best_wall_interval_sec"`
EmpiricalBestCostSec   float64 `json:"empirical_best_cost_interval_sec"`
MinMeanWallSec         float64 `json:"min_mean_wall_sec"`
MinSpotTotalUSD        float64 `json:"min_spot_total_usd"`
}

type Stage7Report struct {
Label          string                  `json:"label"`
Calibration    sim.CalibratedConstants `json:"calibration"`
TolerancePct   float64                 `json:"tolerance_pct"`
ValidationPass bool                    `json:"validation_passed"`
Validation     []sim.ValidationCase    `json:"validation_cases"`
Comparisons    []RateComparison        `json:"young_daly_comparisons"`
Sweep          []sim.SweepPoint        `json:"sweep_points"`
}

func main() {
tolPct := flag.Float64("tolerance-pct", 5.0, "Pre-stated validation tolerance percentage")
trials := flag.Int("trials", 1000, "Seeded Poisson Monte Carlo trials per cell")
jsonOut := flag.String("json-out", "results/stage7-simulated-sweep.json", "Output JSON path")
csvOut := flag.String("csv-out", "results/stage7-simulated-sweep.csv", "Output CSV path")
flag.Parse()

cal := sim.MeasuredCalibration()
valCases, passed := sim.ValidateAgainstRealRuns(cal, *tolPct)
fmt.Println("=== STAGE 7 STEP 1: SIMULATED VALIDATION AGAINST REAL AWS RUNS ===")
for _, vc := range valCases {
fmt.Println(sim.SummaryString(vc))
}
if !passed {
fmt.Fprintln(os.Stderr, "SIMULATED_VALIDATION FAILED — aborting sweep per Stage 7 protocol.")
os.Exit(1)
}

rates := []float64{0.25, 0.5, 1.0, 2.0, 4.0, 8.0}
intervals := []float64{5, 10, 15, 20, 30, 45, 60, 120, 300, 600, 0}
regimes := []struct {
name string
prob float64
}{
{"SIMULATED_CRASH_STOP_NO_NOTICE", 0.0},
{"SIMULATED_WITH_IMDSV2_NOTICE_95PCT", 0.95},
}

var sweep []sim.SweepPoint
var comps []RateComparison

for rIdx, reg := range regimes {
for iIdx, rate := range rates {
var bestWallPt, bestCostPt sim.SweepPoint
for jIdx, iv := range intervals {
pt := sim.SimulatePoissonCell(cal, reg.name, rate, iv, reg.prob, *trials, int64(7000+rIdx*1000+iIdx*100+jIdx))
sweep = append(sweep, pt)
if jIdx == 0 || pt.MeanWallSec < bestWallPt.MeanWallSec {
bestWallPt = pt
}
if jIdx == 0 || pt.SpotTotalUSD < bestCostPt.SpotTotalUSD {
bestCostPt = pt
}
}
comps = append(comps, RateComparison{
Label:                "SIMULATED",
Regime:               reg.name,
EvictionsPerHour:     rate,
MTBISec:              bestWallPt.MTBISec,
YoungDalySec:         bestWallPt.YoungDalySec,
EmpiricalBestWallSec: bestWallPt.CheckpointEverySec,
EmpiricalBestCostSec: bestCostPt.CheckpointEverySec,
MinMeanWallSec:       bestWallPt.MeanWallSec,
MinSpotTotalUSD:      bestCostPt.SpotTotalUSD,
})
}
}

fmt.Println("\n=== STAGE 7 STEP 2: SIMULATED EMPIRICAL OPTIMUM VS YOUNG/DALY FORMULA ===")
for _, cp := range comps {
fmt.Printf("[%s] %-35s rate=%4.2f/hr MTBI=%6.0fs | Young-Daly=%5.1fs | SimBestWall=%5.0fs (%.2fs) | SimBestCost=%5.0fs ($%.6f)\n",
cp.Label, cp.Regime, cp.EvictionsPerHour, cp.MTBISec, cp.YoungDalySec, cp.EmpiricalBestWallSec, cp.MinMeanWallSec, cp.EmpiricalBestCostSec, cp.MinSpotTotalUSD)
}

rep := Stage7Report{
Label:          "SIMULATED",
Calibration:    cal,
TolerancePct:   *tolPct,
ValidationPass: passed,
Validation:     valCases,
Comparisons:    comps,
Sweep:          sweep,
}
raw, _ := json.MarshalIndent(rep, "", "  ")
_ = os.WriteFile(*jsonOut, raw, 0o644)

f, _ := os.Create(*csvOut)
defer f.Close()
w := csv.NewWriter(f)
_ = w.Write([]string{"label", "regime", "evictions_per_hour", "mtbi_sec", "young_daly_sec", "checkpoint_every_sec", "mean_wall_sec", "p95_wall_sec", "mean_compute_sec", "wasted_compute_pct", "s3_put_calls", "spot_total_usd"})
for _, p := range sweep {
_ = w.Write([]string{
p.Label, p.Regime, fmt.Sprintf("%.2f", p.EvictionsPerHour), fmt.Sprintf("%.0f", p.MTBISec),
fmt.Sprintf("%.2f", p.YoungDalySec), fmt.Sprintf("%.0f", p.CheckpointEverySec),
fmt.Sprintf("%.2f", p.MeanWallSec), fmt.Sprintf("%.2f", p.P95WallSec),
fmt.Sprintf("%.2f", p.MeanComputeSec), fmt.Sprintf("%.2f", p.WastedComputePct),
fmt.Sprintf("%.2f", p.S3PutCalls), strconv.FormatFloat(p.SpotTotalUSD, 'f', 6, 64),
})
}
w.Flush()
}
