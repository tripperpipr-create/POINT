package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"local-agent-workbench/internal/egress"
)

func TestDecodePolicyRequiresValidGatewayContract(t *testing.T) {
	policy, err := egress.Compile("ALLOWLIST", []string{"api.example.com:443"}, egress.Quota{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(policy)
	decoded, err := decodePolicy(base64.RawURLEncoding.EncodeToString(encoded))
	if err != nil || decoded.Digest != policy.Digest {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if _, err = decodePolicy("not-base64"); err == nil {
		t.Fatal("invalid policy encoding was accepted")
	}
}

func TestBoundedID(t *testing.T) {
	value := boundedID(string(make([]byte, 200)))
	if len(value) != 128 {
		t.Fatalf("bounded ID length=%d", len(value))
	}
}
