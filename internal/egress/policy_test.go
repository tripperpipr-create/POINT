package egress

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestCompileIsExactCanonicalAndStable(t *testing.T) {
	first, err := Compile("DENY", []string{"tls://API.Example.com:8443", "registry.npmjs.org", "api.example.com:8443"}, Quota{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile("ALLOWLIST", []string{"registry.npmjs.org:443", "api.example.com:8443"}, DefaultQuota())
	if err != nil {
		t.Fatal(err)
	}
	if first.Mode != "ALLOWLIST" || len(first.Rules) != 2 || first.Digest != second.Digest {
		t.Fatalf("canonical policy mismatch: first=%+v second=%+v", first, second)
	}
	if !first.Allows("API.EXAMPLE.COM.", 8443, "tls") || first.Allows("evilapi.example.com", 8443, "tls") || first.Allows("api.example.com", 443, "tls") {
		t.Fatal("exact FQDN/port/protocol match failed")
	}
}

func TestCompileRejectsUnboundedAndAmbiguousRules(t *testing.T) {
	invalid := []string{"1.2.3.4", "*.example.com", "http://example.com:80", "example", "https://user@example.com/path"}
	for _, value := range invalid {
		if _, err := Compile("ALLOWLIST", []string{value}, Quota{}); err == nil {
			t.Fatalf("accepted unsafe rule %q", value)
		}
	}
	if _, err := Compile("ALLOW", nil, Quota{}); err == nil || !strings.Contains(err.Error(), "unrestricted") {
		t.Fatalf("unrestricted policy was not rejected: %v", err)
	}
}

type fixedResolver struct {
	addresses []net.IPAddr
}

func (r fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addresses, nil
}

func TestResolvePinnedRejectsPrivateOrMixedDNSAnswers(t *testing.T) {
	public := net.ParseIP("8.8.8.8")
	private := net.ParseIP("169.254.169.254")
	resolved, err := ResolvePinned(context.Background(), fixedResolver{addresses: []net.IPAddr{{IP: public}}}, "api.example.com")
	if err != nil || !resolved.Equal(public) {
		t.Fatalf("public resolution: ip=%v err=%v", resolved, err)
	}
	for _, addresses := range [][]net.IPAddr{{{IP: private}}, {{IP: public}, {IP: private}}, {{IP: net.ParseIP("127.0.0.1")}}} {
		if _, err = ResolvePinned(context.Background(), fixedResolver{addresses: addresses}, "api.example.com"); err == nil {
			t.Fatalf("accepted rebinding/private DNS answer: %+v", addresses)
		}
	}
	if _, err = ResolvePinned(context.Background(), fixedResolver{addresses: []net.IPAddr{{IP: public}}}, "8.8.8.8"); err == nil {
		t.Fatal("accepted literal IP destination")
	}
}
