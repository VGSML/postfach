package screen

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"
)

// LanguageGate flags text in languages outside a configured allowlist.
// Rationale: the injection classifier only covers the languages it was
// trained on — an injection in, say, Chinese would sail through it, so
// "cannot screen" must mean "do not trust".
//
// Two checks:
//  1. Script gate (always): letters from scripts none of the allowed
//     languages use (Han in a German invoice mailbox, etc.). Deterministic
//     and per-rune, so it also catches a foreign-script fragment embedded
//     in an otherwise allowed-language text.
//  2. Statistical language detection (texts with >= langIDMinLetters
//     letters): catches disallowed languages written in an allowed script,
//     e.g. Polish in a Latin-only allowlist.
type LanguageGate struct {
	allowed        map[string]bool // ISO 639-3
	allowedScripts []*unicode.RangeTable
	scriptNames    map[*unicode.RangeTable]string
	detectOptions  whatlanggo.Options
}

const (
	// foreignScriptMinRunes tolerates a few stray glyphs (a company name in
	// CJK inside a German invoice) without flagging.
	foreignScriptMinRunes = 10
	langIDMinLetters      = 60
)

// iso1to3 maps common ISO 639-1 codes to the 639-3 codes whatlanggo uses.
var iso1to3 = map[string]string{
	"en": "eng", "de": "deu", "ru": "rus", "fr": "fra", "es": "spa",
	"it": "ita", "pt": "por", "nl": "nld", "pl": "pol", "uk": "ukr",
	"cs": "ces", "sk": "slk", "tr": "tur", "sv": "swe", "da": "dan",
	"no": "nob", "fi": "fin", "hu": "hun", "ro": "ron", "bg": "bul",
	"el": "ell", "he": "heb", "ar": "arb", "hi": "hin", "th": "tha",
	"zh": "cmn", "ja": "jpn", "ko": "kor", "vi": "vie", "id": "ind",
	"lt": "lit", "lv": "lav", "et": "est", "sl": "slv", "hr": "hrv",
}

// langScripts lists the scripts each supported language is written in.
// Latin/Cyrillic/etc. tables come from the unicode package.
var langScripts = map[string][]*unicode.RangeTable{
	"eng": {unicode.Latin}, "deu": {unicode.Latin}, "fra": {unicode.Latin},
	"spa": {unicode.Latin}, "ita": {unicode.Latin}, "por": {unicode.Latin},
	"nld": {unicode.Latin}, "pol": {unicode.Latin}, "ces": {unicode.Latin},
	"slk": {unicode.Latin}, "tur": {unicode.Latin}, "swe": {unicode.Latin},
	"dan": {unicode.Latin}, "nob": {unicode.Latin}, "fin": {unicode.Latin},
	"hun": {unicode.Latin}, "ron": {unicode.Latin}, "vie": {unicode.Latin},
	"ind": {unicode.Latin}, "lit": {unicode.Latin}, "lav": {unicode.Latin},
	"est": {unicode.Latin}, "slv": {unicode.Latin}, "hrv": {unicode.Latin},
	"rus": {unicode.Cyrillic}, "ukr": {unicode.Cyrillic}, "bul": {unicode.Cyrillic},
	"ell": {unicode.Greek},
	"heb": {unicode.Hebrew},
	"arb": {unicode.Arabic},
	"hin": {unicode.Devanagari},
	"tha": {unicode.Thai},
	"cmn": {unicode.Han},
	"jpn": {unicode.Han, unicode.Hiragana, unicode.Katakana},
	"kor": {unicode.Hangul, unicode.Han},
}

// knownScripts is what the script gate can name; letters from any other
// script count as foreign too.
var knownScripts = map[*unicode.RangeTable]string{
	unicode.Latin: "Latin", unicode.Cyrillic: "Cyrillic", unicode.Greek: "Greek",
	unicode.Han: "Han", unicode.Hiragana: "Hiragana", unicode.Katakana: "Katakana",
	unicode.Hangul: "Hangul", unicode.Arabic: "Arabic", unicode.Hebrew: "Hebrew",
	unicode.Devanagari: "Devanagari", unicode.Thai: "Thai",
}

// NewLanguageGate builds the gate from ISO 639-1 or 639-3 codes.
func NewLanguageGate(langs []string) (*LanguageGate, error) {
	g := &LanguageGate{
		allowed:     map[string]bool{},
		scriptNames: knownScripts,
	}
	scriptSet := map[*unicode.RangeTable]bool{}
	for _, l := range langs {
		code := strings.ToLower(strings.TrimSpace(l))
		if code == "" {
			continue
		}
		if c3, ok := iso1to3[code]; ok {
			code = c3
		}
		scripts, ok := langScripts[code]
		if !ok {
			return nil, fmt.Errorf("POSTFACH_ALLOWED_LANGS: unsupported language %q", l)
		}
		g.allowed[code] = true
		for _, s := range scripts {
			scriptSet[s] = true
		}
	}
	if len(g.allowed) == 0 {
		return nil, fmt.Errorf("POSTFACH_ALLOWED_LANGS: no languages configured")
	}
	for s := range scriptSet {
		g.allowedScripts = append(g.allowedScripts, s)
	}
	return g, nil
}

func (g *LanguageGate) Name() string { return "language" }

func (g *LanguageGate) Screen(_ context.Context, text string) (Verdict, error) {
	var v Verdict

	// 1. Script gate.
	foreign := map[string]int{}
	letters := 0
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		ok := false
		for _, s := range g.allowedScripts {
			if unicode.Is(s, r) {
				ok = true
				break
			}
		}
		if ok {
			continue
		}
		name := "other"
		for table, n := range g.scriptNames {
			if unicode.Is(table, r) {
				name = n
				break
			}
		}
		foreign[name]++
	}
	total := 0
	names := make([]string, 0, len(foreign))
	for n, c := range foreign {
		total += c
		names = append(names, fmt.Sprintf("%s(%d)", n, c))
	}
	if total >= foreignScriptMinRunes {
		sort.Strings(names)
		v.Flagged = true
		v.Reasons = append(v.Reasons,
			"language:foreign script outside allowlist: "+strings.Join(names, ", "))
	}

	// 2. Statistical language ID for texts long enough to be reliable.
	if letters >= langIDMinLetters {
		info := whatlanggo.Detect(text)
		code := whatlanggo.LangToString(info.Lang)
		if info.IsReliable() && code != "" && !g.allowed[code] {
			v.Flagged = true
			v.Reasons = append(v.Reasons,
				fmt.Sprintf("language:detected %q which is not in the allowlist", code))
		}
	}
	return v, nil
}
