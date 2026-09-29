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
