package verification

import (
	"bytes"
	"encoding/json"
	"path"
	"strings"
)

// Identity records the operation, not its explanation or time allowance.
// cwd's root spellings and JSON key order are normalized; executable arguments
// remain intact. Changing reason or timeout cannot erase an earlier failure.
func CheckIdentity(name string, arguments json.RawMessage) string {
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	if len(arguments) == 0 {
		fields = map[string]any{}
	} else if decoder.Decode(&fields) != nil || fields == nil {
		return name + string(arguments)
	}
	delete(fields, "reason")
	if name == "run_command" {
		delete(fields, "timeoutSeconds")
		cwd, _ := fields["cwd"].(string)
		fields["cwd"] = path.Clean(strings.ReplaceAll(cwd, "\\", "/"))
	if cmd, ok := fields["command"].(string); ok {
		cmd = strings.TrimSpace(redirectSuffix.ReplaceAllString(strings.TrimSpace(cmd), ""))
		fields["command"] = cmd
		if normalized := CanonicalPHPUnitCommand(cmd); normalized != "" {
			fields["command"] = normalized
		}
	}
	}
	encoded, _ := json.Marshal(fields)
	return name + string(encoded)
}
