package sim

import "testing"

func TestRealRunValidationGatePassesWithinTolerance(t *testing.T) {
c := MeasuredCalibration()
cases, ok := ValidateAgainstRealRuns(c, 5.0)
if !ok {
for _, vc := range cases {
t.Log(SummaryString(vc))
}
t.Fatalf("expected all 5 real runs to validate within +/-5.0%% tolerance")
}
}
