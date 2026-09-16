package rag

import "testing"

func ev(doc, rev, chunk string, index int, dist float64) Evidence {
	return Evidence{ChunkID: chunk, DocumentID: doc, RevisionID: rev, ChunkIndex: index, Distance: dist}
}

func ranks(out []Evidence) []string {
	keys := []string{}
	for _, e := range out {
		keys = append(keys, e.ChunkID)
	}
	return keys
}

// TEST_PLAN hybrid ordering: score DESC, deterministic ties.
func TestFuseHybridOrdersAndTies(t *testing.T) {
	vectorBranch := []Evidence{
		ev("d1", "r1", "c1", 0, 0.1),
		ev("d1", "r1", "c2", 1, 0.2),
		ev("d2", "r1", "c3", 0, 0.3),
	}
	ftsBranch := []Evidence{
		ev("d1", "r1", "c2", 1, 0), // FTS-only branch (distance zero is meaningless)
		ev("d2", "r1", "c4", 1, 0),
	}
	k := 60.0
	out := fuseHybrid(vectorBranch, ftsBranch, k, 10)

	// c1: 1/(60+1) = 0.016393; c2: both branches = 1/61+1/62 = 0.032597 (top);
	// c3: 1/63 = 0.015873; c4 (FTS only): 1/62 = 0.016129 → order c2, c1, c4, c3.
	want := []string{"c2", "c1", "c4", "c3"}
	got := ranks(out)
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %s, want %s (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestFuseHybridDeduplicates(t *testing.T) {
	vectorBranch := []Evidence{ev("d1", "r1", "c1", 0, 0.1), ev("d1", "r1", "c1", 1, 0.2)}
	ftsBranch := []Evidence{ev("d1", "r1", "c1", 1, 0)}
	out := fuseHybrid(vectorBranch, ftsBranch, 60.0, 5)
	if len(out) != 1 {
		t.Fatalf("duplicate chunk appears %d times, want exactly once", len(out))
	}
	// A branch position is taken from the first occurrence; the second
	// occurrence of the same chunk must not add more fused score once counted.
	if out[0].Rank != 1 || out[0].VectorRank != 1 || out[0].FTSRank != 1 {
		t.Fatalf("fused ranks wrong: %+v", out[0])
	}
}

func TestFuseHybridTopKAfterMerge(t *testing.T) {
	vector := []Evidence{ev("d1", "r", "c1", 0, 0.1)}
	fts := []Evidence{ev("d1", "r", "c9", 3, 0), ev("d1", "r", "c2", 5, 0)}
	out := fuseHybrid(vector, fts, 60.0, 2)
	if len(out) != 2 {
		t.Fatalf("top-k must apply after merge; got %d results", len(out))
	}
	if out[0].ChunkID != "c1" {
		t.Fatalf("c1 is the highest fused rank: %+v", out[0])
	}
}

// Empty-branch behavior: an all-empty result still occurs only when the
// boundary is truly empty; fusion with a missing branch is deterministic.
func TestFuseHybridEmptyBranches(t *testing.T) {
	if got := fuseHybrid(nil, nil, 60.0, 5); len(got) != 0 {
		t.Fatalf("both-empty must fuse to empty, got %v", got)
	}
	vector := []Evidence{ev("d1", "r", "c1", 0, 0.1)}
	out := fuseHybrid(vector, nil, 60.0, 5)
	if len(out) != 1 || out[0].ChunkID != "c1" || out[0].FTSRank != 0 {
		t.Fatalf("fts-empty branch must exclude its contributions: %+v", out[0])
	}
	// Deterministic: same input yields the same order twice.
	again := fuseHybrid(vector, nil, 60.0, 5)
	if ranks(out)[0] != ranks(again)[0] {
		t.Fatal("fusion must be deterministic")
	}
}

func TestFuseHybridTieBreaksByIdentity(t *testing.T) {
	// Same score (e.g. both at the same branch rank in different docs): fused
	// equal ranks → identity order breaks the tie deterministically.
	vector := []Evidence{
		ev("d1", "r1", "c1", 0, 0.5),
		ev("d2", "r1", "c6", 2, 0.5),
	}
	out := fuseHybrid(vector, nil, 60.0, 5)
	if len(out) != 2 || ranks(out)[0] != "c1" {
		t.Fatalf("tie must resolve by document identity: %v", ranks(out))
	}
}
