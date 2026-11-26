package sim

import (
"math"
"math/rand"
"sort"
)

// PricingParams holds real AWS us-east-1 unit prices (USD).
type PricingParams struct {
OnDemandPerHour      float64 `json:"on_demand_per_hour"`
SpotPerHour          float64 `json:"spot_per_hour"`
S3PutPerThousand     float64 `json:"s3_put_per_thousand"`
S3GetPerThousand     float64 `json:"s3_get_per_thousand"`
DDBWritePerMillion   float64 `json:"ddb_write_per_million"`
EBSGBMonth           float64 `json:"ebs_gb_month"`
RootVolumeGB         float64 `json:"root_volume_gb"`
}

func DefaultUSEast1Pricing() PricingParams {
return PricingParams{
OnDemandPerHour:    0.0104,  // t3.micro On-Demand us-east-1
SpotPerHour:        0.0031,  // t3.micro Spot us-east-1 (~70% discount)
S3PutPerThousand:   0.005,   // S3 Standard PUT/LIST
S3GetPerThousand:   0.0004,  // S3 Standard GET
DDBWritePerMillion: 1.25,    // DynamoDB On-Demand WRU
EBSGBMonth:         0.08,    // gp3 EBS
RootVolumeGB:       8.0,
}
}

// JobSpec defines workload + measured infrastructure parameters.
type JobSpec struct {
BaseComputeSec       float64 `json:"base_compute_sec"`       // Uninterrupted compute time (e.g., 7200s = 2h)
RestartOverheadSec   float64 `json:"restart_overhead_sec"`   // Measured Stage 3/4 ASG+boot+load = 13.64s
CheckpointWriteSec   float64 `json:"checkpoint_write_sec"`   // Measured Stage 2/4 S3 PutObject = 0.060s
HeartbeatIntervalSec float64 `json:"heartbeat_interval_sec"` // DynamoDB lease renewal cadence (5s)
GracefulNoticeProb   float64 `json:"graceful_notice_prob"`   // Probability IMDSv2 catches 2-min notice (0.95)
EvictionsPerHour     float64 `json:"evictions_per_hour"`     // Poisson arrival rate lambda
CheckpointEverySec   float64 `json:"checkpoint_every_sec"`   // 0 = disabled (only graceful notice saves state)
Trials               int     `json:"trials"`
Seed                 int64   `json:"seed"`
}

type CellResult struct {
EvictionsPerHour   float64 `json:"evictions_per_hour"`
CheckpointEverySec float64 `json:"checkpoint_every_sec"`
MeanWallSec        float64 `json:"mean_wall_sec"`
P95WallSec         float64 `json:"p95_wall_sec"`
MeanEvictions      float64 `json:"mean_evictions"`
WastedComputePct   float64 `json:"wasted_compute_pct"`
CheckpointWritePct float64 `json:"checkpoint_write_pct"`
S3PutCalls         float64 `json:"s3_put_calls"`
DDBWriteUnits      float64 `json:"ddb_write_units"`
OnDemandTotalUSD   float64 `json:"on_demand_total_usd"`
SpotTotalUSD       float64 `json:"spot_total_usd"`
SavingsPct         float64 `json:"savings_pct"`
}

// RunCell executes Monte Carlo trials for a single (eviction_rate, checkpoint_interval) point.
func RunCell(spec JobSpec, price PricingParams) CellResult {
trials := spec.Trials
if trials <= 0 {
trials = 500
}
rng := rand.New(rand.NewSource(spec.Seed))

walls := make([]float64, trials)
var sumWall, sumEvict, sumWasted, sumCkptTime, sumS3Puts, sumDDBWrites float64

lambdaPerSec := spec.EvictionsPerHour / 3600.0

for t := 0; t < trials; t++ {
completedWork := 0.0
lastCkptWork := 0.0
sinceLastCkpt := 0.0
wall := 0.0
evictions := 0.0
wasted := 0.0
ckptTime := 0.0
s3Puts := 0.0
ddbWrites := 1.0 // initial AcquireLease

for completedWork < spec.BaseComputeSec {
remWork := spec.BaseComputeSec - completedWork
timeToNextCkpt := math.Inf(1)
if spec.CheckpointEverySec > 0 {
timeToNextCkpt = spec.CheckpointEverySec - sinceLastCkpt
if timeToNextCkpt < 1e-6 {
timeToNextCkpt = spec.CheckpointEverySec
}
}

timeToEvict := math.Inf(1)
if lambdaPerSec > 0 {
timeToEvict = rng.ExpFloat64() / lambdaPerSec
}

step := math.Min(remWork, math.Min(timeToNextCkpt, timeToEvict))
completedWork += step
sinceLastCkpt += step
wall += step
if spec.HeartbeatIntervalSec > 0 {
ddbWrites += step / spec.HeartbeatIntervalSec
}

if completedWork >= spec.BaseComputeSec {
// Final completion checkpoint + lease release
wall += spec.CheckpointWriteSec
ckptTime += spec.CheckpointWriteSec
s3Puts += 1
ddbWrites += 2 // pre-write verify + release
break
}

if timeToEvict <= timeToNextCkpt && timeToEvict <= remWork {
evictions++
if rng.Float64() < spec.GracefulNoticeProb {
// Graceful IMDSv2 notice: immediate emergency checkpoint flush, zero wasted work
wall += spec.CheckpointWriteSec
ckptTime += spec.CheckpointWriteSec
s3Puts += 1
ddbWrites += 2 // verify + release
lastCkptWork = completedWork
sinceLastCkpt = 0
} else {
// Sudden hard termination without notice: rollback to lastCkptWork
lost := completedWork - lastCkptWork
wasted += lost
completedWork = lastCkptWork
sinceLastCkpt = 0
}
// Replacement instance boot + state load + lease acquire
wall += spec.RestartOverheadSec
ddbWrites += 1
} else if timeToNextCkpt <= remWork {
// Periodic checkpoint save
wall += spec.CheckpointWriteSec
ckptTime += spec.CheckpointWriteSec
s3Puts += 1
ddbWrites += 1 // synchronous pre-write fencing check
lastCkptWork = completedWork
sinceLastCkpt = 0
}
}

walls[t] = wall
sumWall += wall
sumEvict += evictions
sumWasted += wasted
sumCkptTime += ckptTime
sumS3Puts += s3Puts
sumDDBWrites += ddbWrites
}

sort.Float64s(walls)
meanWall := sumWall / float64(trials)
p95Wall := walls[int(float64(trials)*0.95)]
meanS3Puts := sumS3Puts / float64(trials)
meanDDB := sumDDBWrites / float64(trials)

ebsPerSec := (price.EBSGBMonth * price.RootVolumeGB) / (30.0 * 24.0 * 3600.0)
odCompute := (spec.BaseComputeSec / 3600.0) * price.OnDemandPerHour
odEBS := spec.BaseComputeSec * ebsPerSec
odTotal := odCompute + odEBS

spotCompute := (meanWall / 3600.0) * price.SpotPerHour
spotEBS := meanWall * ebsPerSec
s3Cost := (meanS3Puts/1000.0)*price.S3PutPerThousand + ((sumEvict/float64(trials)+1)/1000.0)*price.S3GetPerThousand
ddbCost := (meanDDB / 1_000_000.0) * price.DDBWritePerMillion
spotTotal := spotCompute + spotEBS + s3Cost + ddbCost

savingsPct := ((odTotal - spotTotal) / odTotal) * 100.0

return CellResult{
EvictionsPerHour:   spec.EvictionsPerHour,
CheckpointEverySec: spec.CheckpointEverySec,
MeanWallSec:        round2(meanWall),
P95WallSec:         round2(p95Wall),
MeanEvictions:      round2(sumEvict / float64(trials)),
WastedComputePct:   round2((sumWasted / float64(trials) / spec.BaseComputeSec) * 100.0),
CheckpointWritePct: round2((sumCkptTime / float64(trials) / spec.BaseComputeSec) * 100.0),
S3PutCalls:         round2(meanS3Puts),
DDBWriteUnits:      round2(meanDDB),
OnDemandTotalUSD:   round6(odTotal),
SpotTotalUSD:       round6(spotTotal),
SavingsPct:         round2(savingsPct),
}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
