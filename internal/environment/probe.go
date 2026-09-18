package environment

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"
)

// ProbeNetworkHosts resolves and dials TLS :443 for each host. Returns unreachable
// hosts with a short reason. Used before composer/agent loops so intake fails early.
func ProbeNetworkHosts(ctx context.Context, hosts []string) []string {
	var unreachable []string
	seen := map[string]bool{}
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		host = strings.TrimSuffix(host, ".")
		if idx := strings.Index(host, ":"); idx >= 0 {
			host = host[:idx]
		}
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		if err := probeHostTLS(ctx, host); err != nil {
			unreachable = append(unreachable, fmt.Sprintf("%s (%s)", host, err.Error()))
		}
	}
	return unreachable
}

func probeHostTLS(ctx context.Context, host string) error {
	resolver := &net.Resolver{}
	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	addrs, err := resolver.LookupHost(lookupCtx, host)
	if err != nil {
		return fmt.Errorf("dns: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("dns: no addresses")
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	tlsCtx, tlsCancel := context.WithTimeout(ctx, 10*time.Second)
	defer tlsCancel()
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, "443"), &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
	})
	if err != nil {
		// Prefer context deadline messaging.
		select {
		case <-tlsCtx.Done():
			return fmt.Errorf("tls: %w", tlsCtx.Err())
		default:
			return fmt.Errorf("tls: %w", err)
		}
	}
	_ = conn.Close()
	return nil
}
