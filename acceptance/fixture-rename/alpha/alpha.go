package alpha

import "local-agent-workbench/acceptance/fixture-rename/shared"

func Double(n int) int { return shared.Helper(n) + shared.Helper(n) }
