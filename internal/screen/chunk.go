package screen

// chunkIDs splits a token-id sequence into windows of at most size tokens,
// with overlap tokens shared between consecutive windows, so an injection
// spanning a window boundary is still seen in one piece. size must be
// greater than overlap.
func chunkIDs(ids []uint32, size, overlap int) [][]uint32 {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) <= size {
		return [][]uint32{ids}
	}
	step := size - overlap
	var out [][]uint32
	for start := 0; start < len(ids); start += step {
		end := min(start+size, len(ids))
		out = append(out, ids[start:end])
		if end == len(ids) {
			break
		}
	}
	return out
}
