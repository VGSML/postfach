package tools

import (
	"strings"
	"testing"
)

func TestPageText(t *testing.T) {
	s := "абвгдеёжзи" // 10 runes, multi-byte

	page, total, next, more := pageText(s, 0, 4)
	if page != "абвг" || total != 10 || next != 4 || !more {
		t.Errorf("first page: %q total=%d next=%d more=%v", page, total, next, more)
	}
	page, _, next, more = pageText(s, 4, 4)
	if page != "деёж" || next != 8 || !more {
		t.Errorf("second page: %q next=%d more=%v", page, next, more)
	}
	page, _, next, more = pageText(s, 8, 4)
	if page != "зи" || next != 10 || more {
		t.Errorf("last page: %q next=%d more=%v", page, next, more)
	}
	// Out-of-range offset clamps to empty page.
	page, _, _, more = pageText(s, 100, 4)
	if page != "" || more {
		t.Errorf("past-end page: %q more=%v", page, more)
	}
	// Negative offset clamps to 0; zero limit uses the default.
	page, _, _, _ = pageText(s, -5, 0)
	if page != s {
		t.Errorf("default limit page: %q", page)
	}
}

func TestPageTextCapsLimit(t *testing.T) {
	s := strings.Repeat("a", maxPageChars+100)
	page, _, next, more := pageText(s, 0, maxPageChars+100)
	if len(page) != maxPageChars || next != maxPageChars || !more {
		t.Errorf("cap: len=%d next=%d more=%v", len(page), next, more)
	}
}
