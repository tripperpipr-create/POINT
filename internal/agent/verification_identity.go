package agent

import (
	"encoding/json"
	"local-agent-workbench/internal/verification"
)

type verificationAttempt struct {
	Revision       int
	Passed         bool
	Detail         string
	Tool           string
	Arguments      json.RawMessage
	ExitCode       *int
	TimedOut       bool
	Structured     bool
	ResultOK       bool
	EverPassed     bool
	BaselineFailed bool
}

func verificationIdentity(name string, arguments json.RawMessage) string {
	return verification.CheckIdentity(name, arguments)
}
