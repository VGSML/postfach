package screen

import "testing"

func seq(n int) []uint32 {
	ids := make([]uint32, n)
	for i := range ids {
		ids[i] = uint32(i)
	}
	return ids
}

func TestChunkIDs(t *testing.T) {
	if got := chunkIDs(nil, 10, 2); got != nil {
		t.Errorf("empty input: got %v", got)
	}
	if got := chunkIDs(seq(10), 10, 2); len(got) != 1 || len(got[0]) != 10 {
		t.Errorf("exact fit: got %d chunks", len(got))
	}

	chunks := chunkIDs(seq(25), 10, 3)
	// step 7: [0:10) [7:17) [14:24) [21:25)
	if len(chunks) != 4 {
		t.Fatalf("got %d chunks, want 4", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 10 {
			t.Errorf("chunk %d has %d tokens, max 10", i, len(c))
		}
	}
	// Every token must be covered.
	covered := map[uint32]bool{}
	for _, c := range chunks {
		for _, id := range c {
			covered[id] = true
		}
	}
	if len(covered) != 25 {
		t.Errorf("covered %d of 25 tokens", len(covered))
	}
	// Consecutive chunks overlap by 3.
	if chunks[1][0] != 7 {
		t.Errorf("second chunk starts at %d, want 7", chunks[1][0])
	}
}
