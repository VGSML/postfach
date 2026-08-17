package screen

import (
	"context"
	"regexp"
)

// Heuristic is the first, cheap screening layer: regex rules for common
// prompt-injection phrasings in English, Russian and German, plus detection
// of invisible-character obfuscation. It is intentionally biased towards
// recall — the cost of a false positive is one extra confirmation step.
type Heuristic struct{}

func NewHeuristic() *Heuristic { return &Heuristic{} }

func (h *Heuristic) Name() string { return "heuristic" }

type rule struct {
	name string
	re   *regexp.Regexp
}

var rules = []rule{
	{"instruction_override_en", regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override)\b[^.\n]{0,60}\b(previous|prior|above|earlier|all)\b[^.\n]{0,40}\b(instructions?|prompts?|rules?|messages?|directives?)\b`)},
	{"instruction_override_ru", regexp.MustCompile(`(?i)(игнорируй|забудь|отбрось|не\s+учитывай|отмени)[^.\n]{0,60}(предыдущ|прежн|выше|все)[^.\n]{0,40}(инструкц|правил|указан|промпт)`)},
	{"instruction_override_de", regexp.MustCompile(`(?i)\b(ignoriere|vergiss|missachte|übergehe)\b[^.\n]{0,60}\b(vorherige[nrs]?|bisherige[nrs]?|obige[nrs]?|alle[ns]?)\b[^.\n]{0,40}\b(anweisungen|instruktionen|regeln)`)},
	{"system_prompt_marker", regexp.MustCompile(`(?i)(<\s*/?\s*system\s*>|\[\s*/?\s*system\s*\]|\bsystem\s+prompt\b|begin\s+system|системн\w*\s+промпт)`)},
	{"role_reassignment", regexp.MustCompile(`(?i)\b(you\s+are\s+now|act\s+as\s+(an?\s+)?(ai|assistant|system|developer)|pretend\s+to\s+be|developer\s+mode|jailbreak|\bDAN\b)|(теперь\s+ты|притворись|представь,?\s+что\s+ты)`)},
	{"addressing_assistant", regexp.MustCompile(`(?i)\b(dear|attention|hey|hi|hello)[,:\s]+\s*(ai|assistant|claude|chatgpt|gpt|llm|copilot|agent)\b`)},
	{"new_instructions", regexp.MustCompile(`(?i)\b(new|updated|important)\s+(instructions?|task|directive)\s*[:!]|\byou\s+must\s+now\b`)},
	{"conceal_from_user", regexp.MustCompile(`(?i)(do\s+not\s+(tell|show|inform|mention)\s+(this\s+to\s+)?the\s+user|hide\s+this\s+from|without\s+(informing|telling)\s+the\s+user|не\s+сообщай\s+пользователю|не\s+говори\s+пользователю)`)},
	{"credential_exfiltration", regexp.MustCompile(`(?i)\b(send|forward|post|upload|exfiltrate)\b[^.\n]{0,60}\b(passwords?|credentials?|secrets?|api\s*keys?|tokens?)\b`)},
	{"markdown_image_exfil", regexp.MustCompile(`!\[[^\]]*\]\(\s*https?://`)},
	{"tool_call_injection", regexp.MustCompile(`(?i)(<\s*(tool_call|function_call|antml:invoke)\b|"tool_calls"\s*:)`)},
}

func (h *Heuristic) Screen(_ context.Context, text string) (Verdict, error) {
	var v Verdict
	if containsInvisible(text) {
		v.Flagged = true
		v.Reasons = append(v.Reasons, "heuristic:invisible_characters")
	}
	for _, r := range rules {
		if r.re.MatchString(text) {
			v.Flagged = true
			v.Reasons = append(v.Reasons, "heuristic:"+r.name)
		}
	}
	return v, nil
}
