package verification

import (
	"testing"
)

func TestCheckIdentityCanonicalizesPHPUnit(t *testing.T) {
	a := CheckIdentity("run_command", []byte(`{"command":"php bin/phpunit","reason":"tests"}`))
	b := CheckIdentity("run_command", []byte(`{"command":"vendor/bin/phpunit 2>&1","reason":"other"}`))
	if a != b {
		t.Fatalf("phpunit identities differ:\n%s\n%s", a, b)
	}
}
