package tools

// Text pagination limits for read_message / read_attachment. Offsets and
// limits count runes ("characters" in the tool contract), not bytes, so a
// page never splits a multi-byte character.
const (
	defaultPageChars = 4000
	maxPageChars     = 40000
)

// pageText returns the [offset, offset+limit) rune window of s together
// with paging metadata.
func pageText(s string, offset, limit int) (page string, totalChars, nextOffset int, hasMore bool) {
	runes := []rune(s)
	totalChars = len(runes)
	offset = min(max(offset, 0), totalChars)
	if limit <= 0 {
		limit = defaultPageChars
	}
	limit = min(limit, maxPageChars)
	end := min(offset+limit, totalChars)
	return string(runes[offset:end]), totalChars, end, end < totalChars
}
