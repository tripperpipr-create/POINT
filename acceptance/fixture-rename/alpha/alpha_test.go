package alpha_test

import (
	"testing"

	"local-agent-workbench/acceptance/fixture-rename/alpha"
	"local-agent-workbench/acceptance/fixture-rename/beta"
)

func TestRenameCallSites(t *testing.T) {
	if alpha.Double(2) != 4 {
		t.Fatalf("alpha.Double=%d", alpha.Double(2))
	}
	if beta.Triple(2) != 6 {
		t.Fatalf("beta.Triple=%d", beta.Triple(2))
	}
}
