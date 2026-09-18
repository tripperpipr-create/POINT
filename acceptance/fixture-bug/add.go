package broken

// Add is intentionally correct; the test expectation is wrong so acceptance
// can verify a model (or operator) fixes the failing assertion.
func Add(a, b int) int { return a + b }
