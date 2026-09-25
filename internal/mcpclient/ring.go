package mcpclient

import (
	"strings"
	"sync"

	"local-agent-workbench/internal/security"
)

// ring держит последние байты stderr сервера. Сервер пишет туда что угодно,
// включая свой токен в тексте ошибки, поэтому наружу хвост отдаётся только
// зачищенным: общими правилами security.Redact и точными значениями секретов,
// которые ядро само вставило в окружение процесса.
type ring struct {
	mu      sync.Mutex
	buf     []byte
	limit   int
	secrets []string
}

func newRing(limit int, secrets []string) *ring {
	kept := make([]string, 0, len(secrets))
	for _, value := range secrets {
		// Короткое значение совпало бы с обычными словами и изуродовало журнал;
		// такой секрет всё равно ловят общие правила.
		if len(strings.TrimSpace(value)) >= 6 {
			kept = append(kept, value)
		}
	}
	return &ring{limit: limit, secrets: kept}
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if over := len(r.buf) - r.limit; over > 0 {
		r.buf = append([]byte(nil), r.buf[over:]...)
	}
	return len(p), nil
}

// Tail — зачищенный хвост не длиннее max байт (0 — весь буфер).
func (r *ring) Tail(max int) string {
	r.mu.Lock()
	text := string(r.buf)
	r.mu.Unlock()
	text = r.scrub(text)
	if max > 0 && len(text) > max {
		text = text[len(text)-max:]
		if cut := strings.IndexByte(text, '\n'); cut >= 0 && cut < len(text)-1 {
			text = text[cut+1:]
		}
	}
	return strings.TrimSpace(text)
}

func (r *ring) scrub(text string) string {
	for _, value := range r.secrets {
		text = strings.ReplaceAll(text, value, "[REDACTED]")
	}
	return security.Redact(text)
}
