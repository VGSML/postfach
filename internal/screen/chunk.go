package screen

// chunkSlice splits items into windows of at most size elements, with
// overlap elements shared between consecutive windows, so a fragment
// spanning a window boundary is still seen in one piece. size must be
// greater than overlap.
func chunkSlice[T any](items []T, size, overlap int) [][]T {
	if len(items) == 0 {
		return nil
	}
	if len(items) <= size {
		return [][]T{items}
	}
	step := size - overlap
	var out [][]T
	for start := 0; start < len(items); start += step {
		end := min(start+size, len(items))
		out = append(out, items[start:end])
		if end == len(items) {
			break
		}
	}
	return out
}

// chunkIDs is chunkSlice for token ids (kept for readability at call sites).
func chunkIDs(ids []uint32, size, overlap int) [][]uint32 {
	return chunkSlice(ids, size, overlap)
}

// chunkRunes splits text into rune windows and returns them as strings.
func chunkRunes(s string, size, overlap int) []string {
	runes := chunkSlice([]rune(s), size, overlap)
	out := make([]string, len(runes))
	for i, r := range runes {
		out[i] = string(r)
	}
	return out
}
