// Общий слой запроса: журнал, время ответа, статус.
package httpapi

import (
	"net/http"
	"strings"
	"time"

	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

type statusRecorder struct {
	http.ResponseWriter
	status  int
	errCode string
	errMsg  string
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) finalStatus() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if requestID == "" {
			requestID = observability.NewRequestID()
		}
		w.Header().Set("X-Request-Id", requestID)
		r = r.WithContext(observability.WithRequestID(r.Context(), requestID))
		log := observability.From(r.Context())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if s.allowedOrigin != "" {
			origin := r.Header.Get("Origin")
			if origin == s.allowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// MCP живёт под своим ключом: он выдан одной сущности на время одного
		// разговора и уходит в чужой процесс. Общий ключ ядра туда отдавать
		// нельзя — им открывается всё API, а исполнителю нужен один маршрут.
		if s.apiToken != "" && r.URL.Path != "/api/health" && r.URL.Path != "/mcp" {
			provided := extractBearerToken(r.Header.Get("Authorization"), r.URL.Query().Get("access_token"))
			if !tokenMatches(s.apiToken, provided) {
				s.problem(w, http.StatusUnauthorized, "unauthorized", "valid API token required")
				log.Warn("http unauthorized", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodDelete && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			s.problem(w, http.StatusUnsupportedMediaType, "content_type", "Content-Type must be application/json")
			log.Warn("http bad content-type", "method", r.Method, "path", r.URL.Path, "content_type", r.Header.Get("Content-Type"))
			return
		}
		bodyLimit := int64(2 << 20)
		// A PNG/JPEG/WebP source may contain up to 16 MiB before base64
		// expansion. Only the immutable source-intake route gets the larger
		// envelope; every other API request keeps the narrow default.
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/sources/preview" {
			bodyLimit = 24 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.finalStatus()
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
			"remote", r.RemoteAddr,
		}
		if recorder.errCode != "" {
			attrs = append(attrs, "error_code", recorder.errCode, "error", observability.Snippet(security.Redact(recorder.errMsg), 400))
		}
		switch {
		case status >= 500:
			log.Error("http request", attrs...)
		case status >= 400:
			log.Warn("http request", attrs...)
		case observability.QuietHTTP(r.Method, r.URL.Path):
			log.Debug("http request", attrs...)
		default:
			log.Info("http request", attrs...)
		}
	})
}
