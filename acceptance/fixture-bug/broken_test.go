//go:build acceptance_fixture

package broken

import "testing"

// Intentional failing test for acceptance task 2.
func TestAcceptanceBug(t *testing.T) {
	if Add(2, 2) != 5 {
		t.Fatalf("Add(2,2)=%d want 5 (intentional broken expectation for acceptance)", Add(2, 2))
	}
}
