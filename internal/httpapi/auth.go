package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const APITokenFileName = "api-token"

// ResolveAPIToken returns POINT_API_TOKEN when set, otherwise reuses or creates
// a token file under dataDir so local clients (extension, smokes) can discover it.
func ResolveAPIToken(dataDir string) (string, error) {
	if token := strings.TrimSpace(os.Getenv("POINT_API_TOKEN")); token != "" {
		if err := WriteAPITokenFile(dataDir, token); err != nil {
			return "", err
		}
		return token, nil
	}
	path := filepath.Join(dataDir, APITokenFileName)
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate API token: %w", err)
	}
	token := hex.EncodeToString(raw)
	if err := WriteAPITokenFile(dataDir, token); err != nil {
		return "", err
	}
	return token, nil
}

func WriteAPITokenFile(dataDir, token string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir for API token: %w", err)
	}
	path := filepath.Join(dataDir, APITokenFileName)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write API token file: %w", err)
	}
	return nil
}

func extractBearerToken(authorization, queryToken string) string {
	const prefix = "Bearer "
	if strings.HasPrefix(authorization, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	}
	return strings.TrimSpace(queryToken)
}

func tokenMatches(expected, provided string) bool {
	if expected == "" || provided == "" {
		return false
	}
	if len(expected) != len(provided) {
		_ = subtle.ConstantTimeCompare([]byte(expected), []byte(expected))
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}
