package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aashiruu/spot-evict/internal/workload"
)

func TestCheckpointSaveResumeAndFallbackOnCorruption(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := &FileStore{Dir: dir}

	state := workload.NewState(30, 100)
	for i := 0; i < 10; i++ {
		if err := workload.ComputeChunk(&state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Save(ctx, "test-run", 1, "w1", state, 512, 3); err != nil {
		t.Fatal(err)
	}
	savedChunk10Hash := state.RunningHashHex

	for i := 0; i < 10; i++ {
		if err := workload.ComputeChunk(&state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Save(ctx, "test-run", 1, "w1", state, 512, 3); err != nil {
		t.Fatal(err)
	}

	// Corrupt the newest checkpoint (chunk 20)
	entries, err := os.ReadDir(filepath.Join(dir, "test-run"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	newestPath := filepath.Join(dir, "test-run", names[len(names)-1])
	raw, err := os.ReadFile(newestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(newestPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	// LoadLatest should reject chunk 20 and fall back to chunk 10
	recovered, warnings, err := store.LoadLatest(ctx, "test-run")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 corruption warning, got %d: %v", len(warnings), warnings)
	}
	if recovered.State.NextChunk != 10 || recovered.State.RunningHashHex != savedChunk10Hash {
		t.Fatalf("unexpected fallback state: %+v", recovered.State)
	}

	// Resume from chunk 10 to 30 and compare with uninterrupted baseline
	resumed := recovered.State
	if _, err := workload.Run(&resumed); err != nil {
		t.Fatal(err)
	}

	baseline := workload.NewState(30, 100)
	if _, err := workload.Run(&baseline); err != nil {
		t.Fatal(err)
	}
	if resumed.RunningHashHex != baseline.RunningHashHex {
		t.Fatalf("final hash mismatch after fallback resume: %s vs %s", resumed.RunningHashHex, baseline.RunningHashHex)
	}
}
