package workload

import "testing"

func TestWorkloadReproducibility(t *testing.T) {
	s1 := NewState(25, 500)
	if _, err := Run(&s1); err != nil {
		t.Fatalf("run 1 failed: %v", err)
	}

	s2 := NewState(25, 500)
	if _, err := Run(&s2); err != nil {
		t.Fatalf("run 2 failed: %v", err)
	}

	if s1.RunningHashHex != s2.RunningHashHex {
		t.Fatalf("checksum mismatch across identical runs: %s vs %s", s1.RunningHashHex, s2.RunningHashHex)
	}
}

func TestSkippedOrRepeatedChunkChangesHash(t *testing.T) {
	baseline := NewState(10, 100)
	if _, err := Run(&baseline); err != nil {
		t.Fatalf("baseline failed: %v", err)
	}

	// Skip chunk 5
	skipped := NewState(10, 100)
	for skipped.NextChunk < 10 {
		if skipped.NextChunk == 5 {
			skipped.NextChunk++
			continue
		}
		if err := ComputeChunk(&skipped); err != nil {
			t.Fatalf("skipped run failed: %v", err)
		}
	}
	if skipped.RunningHashHex == baseline.RunningHashHex {
		t.Fatal("expected skipped chunk to alter final hash")
	}

	// Repeat chunk 4
	repeated := NewState(10, 100)
	for repeated.NextChunk < 5 {
		_ = ComputeChunk(&repeated)
	}
	repeated.NextChunk = 4
	for repeated.NextChunk < 10 {
		_ = ComputeChunk(&repeated)
	}
	if repeated.RunningHashHex == baseline.RunningHashHex {
		t.Fatal("expected repeated chunk to alter final hash")
	}
}
