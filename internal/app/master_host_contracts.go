package app

func masterTurnTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "interrupted"
}
