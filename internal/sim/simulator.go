package sim

import (
"fmt"
"math"
"math/rand"
"sort"
)

// CalibratedConstants are derived exclusively from real AWS measurements in Stages 1-6.
type CalibratedConstants struct {
BaseComputeSec       float64 `json:"base_compute_sec"`       // Stage 1 mean: 263.31s (100k chunks @ 379.8 chunks/s)
CheckpointWriteSec   float64 `json:"checkpoint_write_sec"`   // Stage 2/6 mean: 0.080s (S3 PUT + DDB verify)
RestartOverheadSec   float64 `json:"restart_overhead_sec"`   // Stage 3/4 measured: 13.64s (ASG + boot + init + S3 load)
NoticeFlushSec       float64 `json:"notice_flush_sec"`       // Stage 4/6 measured: 0.067s (IMDSv2 notice -> S3 flush)
NoticeWindowSec      float64 `json:"notice_window_sec"`      // AWS Spot ITN window: 120.0s
HeartbeatIntervalSec float64 `json:"heartbeat_interval_sec"` // Stage 5 lease heartbeat: 5.0s
OnDemandPerHour      float64 `json:"on_demand_per_hour"`      // us-east-1 t3.micro: $0.0104/hr
SpotPerHour          float64 `json:"spot_per_hour"`          // us-east-1 t3.micro Spot: $0.0031/hr
S3PutPerThousand     float64 `json:"s3_put_per_thousand"`     // $0.005 / 1,000 PUTs
DDBWritePerMillion   float64 `json:"ddb_write_per_million"`   // $1.25 / 1,000,000 WRUs
}

func MeasuredCalibration() CalibratedConstants {
return CalibratedConstants{
BaseComputeSec:       263.31,
CheckpointWriteSec:   0.080,
RestartOverheadSec:   13.64,
NoticeFlushSec:       0.067,
NoticeWindowSec:      120.0,
HeartbeatIntervalSec: 5.0,
OnDemandPerHour:      0.0104,
SpotPerHour:          0.0031,
S3PutPerThousand:     0.005,
DDBWritePerMillion:   1.25,
}
}

type ValidationCase struct {
Label             string  `json:"label"`
Name              string  `json:"name"`
RealComputeSec    float64 `json:"real_compute_sec"`
SimComputeSec     float64 `json:"sim_compute_sec"`
ErrorPct          float64 `json:"error_pct"`
TolerancePct      float64 `json:"tolerance_pct"`
Passed            bool    `json:"passed"`
}

// ValidateAgainstRealRuns checks simulated completion/compute time against Stage 1 and Stage 6 real runs
// using the fixed schedule (t=45s FIS notice, 120s notice window) before running any sweep.
func ValidateAgainstRealRuns(c CalibratedConstants, tolerancePct float64) ([]ValidationCase, bool) {
// Fixed schedule from Stage 6: FIS sends 2-min notice at t=45s after worker start; hard termination at t=165s.
const noticeAt = 45.0
const periodicIntervalSec = 39.5 // 15,000 chunks / 379.8 chunks/s

simulateFixed := func(usePeriodic, watchNotice bool) float64 {
// Worker 1 runs from t=0 until noticeAt (45s)
if watchNotice {
// Catches notice at t=45s, flushes emergency checkpoint in NoticeFlushSec, exits cleanly.
// Worker 2 resumes from 45s of completed work and runs remaining (BaseComputeSec - 45s).
w1Compute := noticeAt + c.NoticeFlushSec
rem := c.BaseComputeSec - noticeAt
ckpts := 1.0
if usePeriodic {
ckpts += math.Floor(noticeAt/periodicIntervalSec) + math.Floor(rem/periodicIntervalSec)
}
w2Compute := rem + ckpts*c.CheckpointWriteSec
return w1Compute + w2Compute
}
// Ignores notice; keeps computing until hard termination at noticeAt + NoticeWindowSec (165s)
hardTermAt := noticeAt + c.NoticeWindowSec
savedWork := 0.0
ckptsW1 := 0.0
if usePeriodic {
ckptsW1 = math.Floor(hardTermAt / periodicIntervalSec)
savedWork = ckptsW1 * periodicIntervalSec
}
w1Compute := hardTermAt
rem := c.BaseComputeSec - savedWork
ckptsW2 := 0.0
if usePeriodic {
ckptsW2 = math.Floor(rem / periodicIntervalSec)
}
w2Compute := rem + ckptsW2*c.CheckpointWriteSec
return w1Compute + w2Compute
}

realCases := []struct {
name string
real float64
sim  float64
}{
{"Stage1-OnDemand-Baseline (0 interruptions)", 263.31, c.BaseComputeSec},
{"Stage6-StrategyA-None (t=45s FIS)", 427.42, simulateFixed(false, false)},
{"Stage6-StrategyB-NoticeOnly (t=45s FIS)", 261.68, simulateFixed(false, true)},
{"Stage6-StrategyC-Periodic-15k (t=45s FIS)", 270.58, simulateFixed(true, false)},
{"Stage6-StrategyD-Periodic+Notice (t=45s FIS)", 263.46, simulateFixed(true, true)},
}

var out []ValidationCase
allPassed := true
for _, rc := range realCases {
errPct := math.Abs(rc.sim-rc.real) / rc.real * 100.0
ok := errPct <= tolerancePct
if !ok {
allPassed = false
}
out = append(out, ValidationCase{
Label:          "SIMULATED_VALIDATION",
Name:           rc.name,
RealComputeSec: round2(rc.real),
SimComputeSec:  round2(rc.sim),
ErrorPct:       round2(errPct),
TolerancePct:   tolerancePct,
Passed:         ok,
})
}
return out, allPassed
}

type SweepPoint struct {
Label              string  `json:"label"`
Regime             string  `json:"regime"`
EvictionsPerHour   float64 `json:"evictions_per_hour"`
MTBISec            float64 `json:"mtbi_sec"`
YoungDalySec       float64 `json:"young_daly_optimal_sec"`
CheckpointEverySec float64 `json:"checkpoint_every_sec"`
MeanWallSec        float64 `json:"mean_wall_sec"`
P95WallSec         float64 `json:"p95_wall_sec"`
MeanComputeSec     float64 `json:"mean_compute_sec"`
WastedComputePct   float64 `json:"wasted_compute_pct"`
S3PutCalls         float64 `json:"s3_put_calls"`
SpotTotalUSD       float64 `json:"spot_total_usd"`
}

func YoungDalyOptimalSec(ckptCostSec, evictionsPerHour float64) float64 {
if evictionsPerHour <= 0 {
return 0
}
mtbi := 3600.0 / evictionsPerHour
return round2(math.Sqrt(2.0 * ckptCostSec * mtbi))
}

func SimulatePoissonCell(c CalibratedConstants, regime string, evictionsPerHour, ckptEverySec, gracefulProb float64, trials int, seed int64) SweepPoint {
rng := rand.New(rand.NewSource(seed))
lambda := evictionsPerHour / 3600.0
walls := make([]float64, trials)
var sumWall, sumCompute, sumWasted, sumPuts, sumDDB float64

for t := 0; t < trials; t++ {
done := 0.0
saved := 0.0
sinceCkpt := 0.0
wall := 0.0
compute := 0.0
wasted := 0.0
puts := 0.0
ddb := 1.0

for done < c.BaseComputeSec {
rem := c.BaseComputeSec - done
nextCkpt := math.Inf(1)
if ckptEverySec > 0 {
nextCkpt = ckptEverySec - sinceCkpt
if nextCkpt < 1e-6 {
nextCkpt = ckptEverySec
}
}
nextEvict := math.Inf(1)
if lambda > 0 {
nextEvict = rng.ExpFloat64() / lambda
}

step := math.Min(rem, math.Min(nextCkpt, nextEvict))
done += step
sinceCkpt += step
wall += step
compute += step
ddb += step / c.HeartbeatIntervalSec

if done >= c.BaseComputeSec {
if ckptEverySec > 0 {
wall += c.CheckpointWriteSec
compute += c.CheckpointWriteSec
puts += 1
ddb += 2
}
break
}

if nextEvict <= nextCkpt && nextEvict <= rem {
if rng.Float64() < gracefulProb {
wall += c.NoticeFlushSec
compute += c.NoticeFlushSec
puts += 1
ddb += 2
saved = done
sinceCkpt = 0
} else {
lost := done - saved
wasted += lost
done = saved
sinceCkpt = 0
}
wall += c.RestartOverheadSec
ddb += 1
} else if nextCkpt <= rem {
wall += c.CheckpointWriteSec
compute += c.CheckpointWriteSec
puts += 1
ddb += 1
saved = done
sinceCkpt = 0
}
}

walls[t] = wall
sumWall += wall
sumCompute += compute
sumWasted += wasted
sumPuts += puts
sumDDB += ddb
}

sort.Float64s(walls)
meanWall := sumWall / float64(trials)
meanCompute := sumCompute / float64(trials)
meanPuts := sumPuts / float64(trials)
meanDDB := sumDDB / float64(trials)

spotCost := (meanCompute/3600.0)*c.SpotPerHour + (meanPuts/1000.0)*c.S3PutPerThousand + (meanDDB/1_000_000.0)*c.DDBWritePerMillion
mtbi := 0.0
if evictionsPerHour > 0 {
mtbi = 3600.0 / evictionsPerHour
}

return SweepPoint{
Label:              "SIMULATED",
Regime:             regime,
EvictionsPerHour:   evictionsPerHour,
MTBISec:            round2(mtbi),
YoungDalySec:       YoungDalyOptimalSec(c.CheckpointWriteSec, evictionsPerHour),
CheckpointEverySec: ckptEverySec,
MeanWallSec:        round2(meanWall),
P95WallSec:         round2(walls[int(float64(trials)*0.95)]),
MeanComputeSec:     round2(meanCompute),
WastedComputePct:   round2((sumWasted / float64(trials) / c.BaseComputeSec) * 100.0),
S3PutCalls:         round2(meanPuts),
SpotTotalUSD:       round6(spotCost),
}
}

func SummaryString(v ValidationCase) string {
return fmt.Sprintf("[%s] %s: real=%.2fs sim=%.2fs err=%.2f%% (tol=%.1f%%) -> passed=%v",
v.Label, v.Name, v.RealComputeSec, v.SimComputeSec, v.ErrorPct, v.TolerancePct, v.Passed)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
