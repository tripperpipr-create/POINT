package storage

import (
	"encoding/json"
	"strings"
)

func marshalJSON(value any) string {
	raw, _ := json.Marshal(value)
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

func unmarshalJSON(raw string, dest any) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	_ = json.Unmarshal([]byte(raw), dest)
}
