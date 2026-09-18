package beta

import "local-agent-workbench/acceptance/fixture-rename/shared"

func Triple(n int) int { return shared.Helper(n) + shared.Helper(n) + shared.Helper(n) }
