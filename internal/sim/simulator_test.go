package sim

import "testing"

func TestSimulationMonotonicityAndBreakEven(t *testing.T) {
price := DefaultUSEast1Pricing()
base := JobSpec{
BaseComputeSec:       3600,
RestartOverheadSec:   13.64,
CheckpointWriteSec:   0.06,
HeartbeatIntervalSec: 5.0,
GracefulNoticeProb:   0.95,
CheckpointEverySec:   300,
Trials:               300,
Seed:                 42,
}

low := base
low.EvictionsPerHour = 0.5
resLow := RunCell(low, price)

high := base
high.EvictionsPerHour = 6.0
resHigh := RunCell(high, price)

if resHigh.MeanWallSec <= resLow.MeanWallSec {
t.Fatalf("expected higher eviction rate to increase wall time: low=%v high=%v", resLow.MeanWallSec, resHigh.MeanWallSec)
}
if resLow.SavingsPct < 55.0 {
t.Fatalf("expected >55%% savings at 0.5 evictions/hr, got %.2f%%", resLow.SavingsPct)
}
}
