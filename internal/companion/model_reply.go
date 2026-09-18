package companion

import (
	"encoding/json"
	"strings"
)

func extractModelReply(raw string) (string, bool) {
	var loose struct {
		Reply string `json:"reply"`
	}
	try := func(body string) (string, bool) {
		if err := json.Unmarshal([]byte(body), &loose); err != nil {
			return "", false
		}
		reply := strings.TrimSpace(loose.Reply)
		if reply == "" {
			return "", false
		}
		return reply, true
	}
	if reply, ok := try(raw); ok {
		return reply, true
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		return try(raw[start : end+1])
	}
	return "", false
}

// ExtractStreamingReply pulls a growing reply string from a partial companion
// JSON envelope. Incomplete JSON is accepted and Markdown fences are stripped.
func ExtractStreamingReply(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "```") {
		firstBreak := strings.IndexByte(raw, '\n')
		if firstBreak < 0 {
			return "", false
		}
		raw = raw[firstBreak+1:]
		if end := strings.LastIndex(raw, "```"); end >= 0 {
			raw = raw[:end]
		}
		raw = strings.TrimSpace(raw)
	}
	key := `"reply"`
	idx := strings.Index(raw, key)
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimLeft(raw[idx+len(key):], " \t\n\r")
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	rest = strings.TrimLeft(rest[1:], " \t\n\r")
	if rest == "" || rest[0] != '"' {
		return "", false
	}
	var b strings.Builder
	escaped := false
	for i := 1; i < len(rest); i++ {
		ch := rest[i]
		if escaped {
			switch ch {
			case '"', '\\', '/':
				b.WriteByte(ch)
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'u':
				if i+4 >= len(rest) {
					return b.String(), true
				}
				hex := rest[i+1 : i+5]
				var runeVal rune
				for _, h := range hex {
					runeVal <<= 4
					switch {
					case h >= '0' && h <= '9':
						runeVal |= rune(h - '0')
					case h >= 'a' && h <= 'f':
						runeVal |= rune(h - 'a' + 10)
					case h >= 'A' && h <= 'F':
						runeVal |= rune(h - 'A' + 10)
					default:
						return b.String(), true
					}
				}
				b.WriteRune(runeVal)
				i += 4
			default:
				b.WriteByte(ch)
			}
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return b.String(), true
		}
		b.WriteByte(ch)
	}
	return b.String(), true
}

// Предел ответа: та же мера и для обычного разбора, и для спасённого куска,
// иначе спасённый прошёл бы там, где целый отвергают.
const companionMaxReplyRunes = 8000

// Спасённый ответ не может быть короче осмысленной фразы: обрубок в несколько
// символов не сообщает человеку ничего, а выданный за ответ — прячет отказ.
const companionRescuedReplyMinRunes = 40

// rescueTruncatedEnvelope достаёт написанное из конверта, оборванного на
// потолке вывода. Целый JSON сюда не попадает: его разбирает обычный путь, а
// проза без поля reply не спасается вовсе — она значит, что модель отвечала не
// по схеме, и это лечится повторным кругом, а не догадками.
func rescueTruncatedEnvelope(raw string) (modelEnvelope, bool) {
	if strings.TrimSpace(raw) == "" || json.Valid([]byte(raw)) {
		return modelEnvelope{}, false
	}
	reply, ok := ExtractStreamingReply(raw)
	if !ok {
		return modelEnvelope{}, false
	}
	reply = strings.TrimSpace(reply)
	runes := []rune(reply)
	if len(runes) < companionRescuedReplyMinRunes {
		return modelEnvelope{}, false
	}
	if len(runes) > companionMaxReplyRunes {
		reply = strings.TrimSpace(string(runes[:companionMaxReplyRunes]))
	}
	return modelEnvelope{Reply: reply, Level: "suggestion"}, true
}
