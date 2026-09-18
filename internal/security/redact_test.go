package security

import (
	"strings"
	"testing"
)

func TestRedactCommonSecrets(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bearer",
			input: `Authorization: Bearer super-secret-token`,
			want:  `Authorization: Bearer [REDACTED]`,
		},
		{
			name:  "api key assignment",
			input: `api_key=abc123xyz`,
			want:  `api_key=[REDACTED]`,
		},
		{
			name:  "openai sk",
			input: `key sk-abcdefghijklmnopqrst`,
			want:  `key [REDACTED]`,
		},
		{
			name:  "aws access key",
			input: `creds AKIAIOSFODNN7EXAMPLE leftover`,
			want:  `creds [REDACTED] leftover`,
		},
		{
			name:  "github pat",
			input: `token ghp_abcdefghijklmnopqrstuvwxyzABCD leftover`,
			want:  `token [REDACTED] leftover`,
		},
		{
			name:  "pem block",
			input: "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0Z3VS5JJcds3xfn/ygWyF6PZG...\n-----END RSA PRIVATE KEY-----\nafter",
			want:  "before\n[REDACTED PEM]\nafter",
		},
		{
			name:  "jwt",
			input: `auth eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signaturepart`,
			want:  `auth [REDACTED]`,
		},
		{
			name:  "url query secret",
			input: `https://example.com/callback?access_token=secretvalue&x=1`,
			want:  `https://example.com/callback?access_token=[REDACTED]&x=1`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.want {
				t.Fatalf("Redact()\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// Пароль внутри адреса — форма, в которой строка подключения к БД попадает в
// текст ошибки драйвера. Ошибка сохраняется в подключении и показывается на
// экране, где обещано, что секрет наружу не уходит никогда.
func TestRedactHidesCredentialsInsideURL(t *testing.T) {
	cases := map[string]string{
		"dial postgres://admin:s3cret@db.local:5432/app failed": "postgres://admin:[REDACTED]@db.local:5432/app",
		"mysql://root:hunter2@127.0.0.1:3306/shop":              "mysql://root:[REDACTED]@127.0.0.1:3306/shop",
	}
	for input, want := range cases {
		got := Redact(input)
		if !strings.Contains(got, want) {
			t.Fatalf("Redact(%q) = %q, ожидалось вхождение %q", input, got, want)
		}
		for _, secret := range []string{"s3cret", "hunter2"} {
			if strings.Contains(got, secret) {
				t.Fatalf("секрет %q остался в %q", secret, got)
			}
		}
	}
	// Адрес без учётных данных не портится.
	plain := "http://127.0.0.1:11434/v1"
	if Redact(plain) != plain {
		t.Fatalf("обычный адрес изменён: %q", Redact(plain))
	}
}

// Ключ каждого провайдера, который Point предлагает в своём каталоге, обязан
// вычищаться и без подписи «api_key=» рядом: провайдер возвращает 401 с ключом
// в тексте, и этот текст оседает в LastError на экране «Связи».
//
// Gemini и Groq этой проверки не проходили: их форматы не совпадали ни с одним
// правилом, хотя оба провайдера есть в domain.BuiltInProviderCatalog.
func TestRedactsKeyFormatsOfOwnCatalog(t *testing.T) {
	keys := map[string]string{
		"openai":     "sk-proj-abc123def456ghi789jkl012",
		"anthropic":  "sk-ant-api03-abc123def456ghi789",
		"openrouter": "sk-or-v1-abc123def456ghi789jkl",
		"gemini":     "AIzaSyD3aBcDeFgHiJkLmNoPqRsTuVwXyZ01234",
		"groq":       "gsk_abc123def456ghi789jkl012mno345",
		"github":     "ghp_abcdefghijklmnopqrstuvwxyz0123",
		"aws":        "AKIAIOSFODNN7EXAMPLE",
	}
	for provider, key := range keys {
		line := "провайдер вернул 401: ключ " + key + " отклонён"
		got := Redact(line)
		if strings.Contains(got, key) {
			t.Errorf("%s: ключ остался в тексте — %q", provider, got)
		}
		if !strings.Contains(got, "отклонён") {
			t.Errorf("%s: вычищено лишнее, текст потерян — %q", provider, got)
		}
	}
}

// Похожее, но не секрет, портить нельзя: иначе редактирование начнёт съедать
// обычные строки и его перестанут применять там, где оно нужно.
func TestRedactKeepsLookalikes(t *testing.T) {
	safe := []string{
		"файл AIzaBrief.md переименован",
		"gsk_short",
		"идентификатор gskabc123def456ghi789jkl012",
		"версия 1.2.3 собрана",
	}
	for _, line := range safe {
		if got := Redact(line); got != line {
			t.Errorf("обычная строка изменена: %q → %q", line, got)
		}
	}
}
