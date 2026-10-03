package verification

import "testing"

func TestMasksExitCode(t *testing.T) {
	cases := map[string]bool{
		"npm run verify || true":            true,
		"npm test || :":                     true,
		"go test ./... || exit 0":           true,
		"npm run build; exit 0":             true,
		"npm run build; true":               true,
		"npm run verify; echo done":         true,
		"set +e; npm test":                  true,
		"npm test":                          false,
		"npm run build && npm test":         false,
		"npm run verify 2>&1 | tail -50":    false,
		"npm test && echo ok":               false,
		"go test ./... || go test -v ./...": false,
		"test -f dist/index.html || exit 1": false,
		"npm test || exit":                  false,
		"npx eslint . --max-warnings=0":     false,
	}
	for command, want := range cases {
		fragment, got := MasksExitCode(command)
		if got != want {
			t.Errorf("%q: masked=%v (%q), want %v", command, got, fragment, want)
		}
	}
}

// Q08: в cmd.exe нет pipefail, и код конвейера — код последней команды.
// Конвейер вне кавычек находится, `||`, `^|` и `|` в кавычках — нет.
func TestTopLevelPipe(t *testing.T) {
	for command, want := range map[string]bool{
		"npm run verify | findstr ERROR": true,
		"npm ci && npm run verify":       false,
		"npm test || exit 1":             false,
		`findstr "a|b" log.txt`:          false,
		"echo a^|b":                      false,
		"go test ./... 2>&1 | tail -20":  true,
	} {
		if got := TopLevelPipe(command); got != want {
			t.Fatalf("TopLevelPipe(%q) = %v, want %v", command, got, want)
		}
	}
}
