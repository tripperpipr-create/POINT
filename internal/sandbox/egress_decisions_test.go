package sandbox

import (
	"strings"
	"testing"
)

func TestParseEgressDecisionsAcceptsOnlyMatchingBoundedGatewayRecords(t *testing.T) {
	line := `{"msg":"sandbox egress","run_id":"run-1","policy_digest":"sha256:trusted","fqdn":"registry.npmjs.org","port":443,"decision":"allowed","reason":"closed","bytes":128}`
	decisions, err := parseEgressDecisions(line+"\n", "sha256:trusted", "run-1")
	if err != nil || len(decisions) != 1 || decisions[0].FQDN != "registry.npmjs.org" || decisions[0].Bytes != 128 {
		t.Fatalf("valid gateway record rejected: decisions=%#v error=%v", decisions, err)
	}
	for _, bad := range []string{
		strings.Replace(line, `"run-1"`, `"run-2"`, 1),
		strings.Replace(line, `"sha256:trusted"`, `"sha256:other"`, 1),
		strings.Replace(line, `"registry.npmjs.org"`, `"127.0.0.1"`, 1),
		strings.Replace(line, `"allowed"`, `"unknown"`, 1),
	} {
		if _, err := parseEgressDecisions(bad, "sha256:trusted", "run-1"); err == nil {
			t.Fatalf("untrusted gateway record accepted: %s", bad)
		}
	}
	if _, err := parseEgressDecisions(strings.Repeat("x", 512*1024+1), "sha256:trusted", "run-1"); err == nil {
		t.Fatal("oversized gateway log accepted")
	}
}
