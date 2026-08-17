package tools

import "testing"

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"report.pdf", "report.pdf"},
		{"../../etc/passwd", "passwd"},
		{`..\..\windows\system32\evil.dll`, "evil.dll"},
		{"/absolute/path/file.txt", "file.txt"},
		{".hidden", "hidden"},
		{"...", "fallback.bin"},
		{"", "fallback.bin"},
		{"name\x00with\x1fcontrols.txt", "namewithcontrols.txt"},
		{"weird:*?\"<>|.txt", "weird_______.txt"},
		{"счёт за июль.pdf", "счёт за июль.pdf"},
	}
	for _, c := range cases {
		if got := SanitizeFilename(c.in, "fallback.bin"); got != c.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeFilenameTruncates(t *testing.T) {
	long := ""
	for range 300 {
		long += "a"
	}
	got := SanitizeFilename(long+".pdf", "fallback.bin")
	if len(got) > maxFilenameLen {
		t.Errorf("length %d exceeds max %d", len(got), maxFilenameLen)
	}
	if got[len(got)-4:] != ".pdf" {
		t.Errorf("extension lost: %q", got[len(got)-10:])
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"a.txt": true, "a-1.txt": true}
	got := uniqueName("a.txt", func(s string) bool { return taken[s] })
	if got != "a-2.txt" {
		t.Errorf("uniqueName = %q, want a-2.txt", got)
	}
	if got := uniqueName("b.txt", func(s string) bool { return taken[s] }); got != "b.txt" {
		t.Errorf("uniqueName = %q, want b.txt", got)
	}
}
