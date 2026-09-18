package egress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const SchemaVersion = 1

type Rule struct {
	FQDN     string `json:"fqdn"`
	Port     uint16 `json:"port"`
	Protocol string `json:"protocol"`
}

type Quota struct {
	MaxConnections int   `json:"maxConnections"`
	MaxBytes       int64 `json:"maxBytes"`
	MaxDurationSec int   `json:"maxDurationSec"`
}

type Policy struct {
	SchemaVersion int    `json:"schemaVersion"`
	Mode          string `json:"mode"`
	Rules         []Rule `json:"rules"`
	Quota         Quota  `json:"quota"`
	Digest        string `json:"digest"`
}

func DefaultQuota() Quota {
	return Quota{MaxConnections: 32, MaxBytes: 256 * 1024 * 1024, MaxDurationSec: 15 * 60}
}

// Compile translates the legacy profile surface into an exact enforceable
// policy. Host-only entries remain compatible and mean tls://host:443.
func Compile(networkPolicy string, allowed []string, quota Quota) (Policy, error) {
	mode := strings.ToUpper(strings.TrimSpace(networkPolicy))
	if mode == "" {
		mode = "DENY"
	}
	if mode != "DENY" && mode != "ALLOWLIST" && mode != "ALLOW" {
		return Policy{}, fmt.Errorf("unsupported network policy %q", networkPolicy)
	}
	if quota == (Quota{}) {
		quota = DefaultQuota()
	}
	if quota.MaxConnections < 1 || quota.MaxConnections > 1000 || quota.MaxBytes < 1024 || quota.MaxBytes > 16*1024*1024*1024 || quota.MaxDurationSec < 1 || quota.MaxDurationSec > 24*60*60 {
		return Policy{}, errors.New("egress quota is outside safe bounds")
	}
	rules := make([]Rule, 0, len(allowed))
	seen := map[string]bool{}
	for _, value := range allowed {
		rule, err := parseRule(value)
		if err != nil {
			return Policy{}, err
		}
		key := fmt.Sprintf("%s:%d/%s", rule.FQDN, rule.Port, rule.Protocol)
		if !seen[key] {
			seen[key] = true
			rules = append(rules, rule)
		}
	}
	if mode == "ALLOW" && len(rules) == 0 {
		return Policy{}, errors.New("unrestricted egress is unsupported; declare exact FQDN, port, and protocol rules")
	}
	if len(rules) > 0 {
		mode = "ALLOWLIST"
	} else {
		mode = "DENY"
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].FQDN != rules[j].FQDN {
			return rules[i].FQDN < rules[j].FQDN
		}
		if rules[i].Port != rules[j].Port {
			return rules[i].Port < rules[j].Port
		}
		return rules[i].Protocol < rules[j].Protocol
	})
	policy := Policy{SchemaVersion: SchemaVersion, Mode: mode, Rules: rules, Quota: quota}
	policy.Digest = policyDigest(policy)
	return policy, nil
}

func CompileToolPolicies(values map[string]string) (Policy, error) {
	mode := values["network"]
	allowed := []string{}
	for key, value := range values {
		if !strings.EqualFold(strings.TrimSpace(value), "ALLOW") || !strings.HasPrefix(strings.ToLower(key), "network:") {
			continue
		}
		allowed = append(allowed, strings.TrimSpace(key[len("network:"):]))
	}
	return Compile(mode, allowed, Quota{})
}

func (p Policy) Allows(fqdn string, port uint16, protocol string) bool {
	fqdn, err := normalizeFQDN(fqdn)
	if err != nil || !strings.EqualFold(protocol, "tls") || p.Mode != "ALLOWLIST" {
		return false
	}
	for _, rule := range p.Rules {
		if rule.FQDN == fqdn && rule.Port == port && rule.Protocol == "tls" {
			return true
		}
	}
	return false
}

// ValidateGatewayPolicy rejects a correctly hashed but non-canonical document.
// The gateway therefore enforces the same parser and quota bounds as profile
// save and process preparation instead of trusting serialized fields.
func ValidateGatewayPolicy(policy Policy) error {
	if policy.SchemaVersion != SchemaVersion || policy.Mode != "ALLOWLIST" || len(policy.Rules) == 0 {
		return errors.New("gateway requires a non-empty current-schema allowlist")
	}
	rules := make([]string, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		if rule.Protocol != "tls" || rule.Port == 0 {
			return errors.New("gateway policy contains an invalid protocol or port")
		}
		rules = append(rules, fmt.Sprintf("tls://%s:%d", rule.FQDN, rule.Port))
	}
	canonical, err := Compile(policy.Mode, rules, policy.Quota)
	if err != nil {
		return err
	}
	encodedPolicy, _ := json.Marshal(policy)
	encodedCanonical, _ := json.Marshal(canonical)
	if !bytes.Equal(encodedPolicy, encodedCanonical) {
		return errors.New("gateway policy is not canonical or its digest is invalid")
	}
	return nil
}

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// ResolvePinned validates every answer before returning a deterministic IP.
// Callers dial the returned address directly and never re-resolve the FQDN.
func ResolvePinned(ctx context.Context, resolver Resolver, fqdn string) (net.IP, error) {
	fqdn, err := normalizeFQDN(fqdn)
	if err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupIPAddr(ctx, fqdn)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, errors.New("DNS returned no addresses")
	}
	valid := make([]netip.Addr, 0, len(addresses))
	for _, item := range addresses {
		address, ok := netip.AddrFromSlice(item.IP)
		if !ok || !publicAddress(address.Unmap()) {
			return nil, fmt.Errorf("DNS answer contains forbidden address for %s", fqdn)
		}
		valid = append(valid, address.Unmap())
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].Less(valid[j]) })
	return net.IP(valid[0].AsSlice()), nil
}

func parseRule(value string) (Rule, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Rule{}, errors.New("egress FQDN is required")
	}
	if !strings.Contains(value, "://") {
		if strings.Count(value, ":") == 1 {
			value = "tls://" + value
		} else {
			value = "tls://" + value + ":443"
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "tls" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return Rule{}, fmt.Errorf("egress rule must be tls://fqdn:port: %q", value)
	}
	host, err := normalizeFQDN(parsed.Hostname())
	if err != nil {
		return Rule{}, err
	}
	portText := parsed.Port()
	if portText == "" {
		portText = "443"
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return Rule{}, fmt.Errorf("invalid egress port %q", portText)
	}
	return Rule{FQDN: host, Port: uint16(port), Protocol: "tls"}, nil
}

func normalizeFQDN(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if value == "" || len(value) > 253 || net.ParseIP(value) != nil || strings.ContainsAny(value, "*/\\") {
		return "", fmt.Errorf("exact DNS FQDN is required: %q", value)
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("FQDN must contain at least two labels: %q", value)
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid FQDN label in %q", value)
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return "", fmt.Errorf("FQDN must use ASCII or punycode: %q", value)
			}
		}
	}
	return value, nil
}

func policyDigest(policy Policy) string {
	policy.Digest = ""
	encoded, _ := json.Marshal(policy)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

var forbiddenPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"), netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("2001:2::/48"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("ff00::/8"),
}

func publicAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range forbiddenPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
