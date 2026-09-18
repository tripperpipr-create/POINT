package security

import "regexp"

type secretRule struct {
	pattern     *regexp.Regexp
	replacement string
}

var secretRules = []secretRule{
	{regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s"']+`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)\b((?:api[_-]?key|token|password|secret)\s*[:=]\s*)[^\s"'&]+`), "${1}[REDACTED]"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`), "[REDACTED]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "[REDACTED]"},
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{20,}\b`), "[REDACTED]"},
	{regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----[\s\S]+?-----END (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`), "[REDACTED PEM]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)([?&](?:api[_-]?key|token|access_token|auth|password|secret)=)[^&\s"']+`), "${1}[REDACTED]"},
	// Учётные данные внутри адреса: postgres://user:пароль@host/db. Драйверы БД
	// охотно вкладывают строку подключения в текст ошибки, а та сохраняется в
	// подключении и показывается на экране «Связи».
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+):[^/\s@]+@`), "${1}:[REDACTED]@"},
	// Ключи провайдеров из собственного каталога Point. Форматы sk-/ghp_/AKIA
	// правила выше ловили, а Gemini и Groq — нет: их ключи проходили насквозь и
	// оседали в LastError, который показывается на экране «Связи».
	// Не «ровно 35»: ключ нестандартной длины проскочил бы мимо жёсткого счёта,
	// а тридцать с лишним буквенно-цифровых подряд на обычное слово не похожи.
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`), "[REDACTED]"},
	{regexp.MustCompile(`\bgsk_[A-Za-z0-9]{20,}\b`), "[REDACTED]"},
}

func Redact(value string) string {
	out := value
	for _, rule := range secretRules {
		out = rule.pattern.ReplaceAllString(out, rule.replacement)
	}
	return out
}
