package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Gateway struct {
	Policy      Policy
	Resolver    Resolver
	DialContext func(context.Context, string, string) (net.Conn, error)
	Logger      *slog.Logger
	RunID       string
	connections atomic.Int64
	bytes       atomic.Int64
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	started := time.Now()
	host, port, reason, status := g.authorizeRequest(request)
	if reason != "" {
		g.logDecision(host, port, "denied", reason, 0, started)
		http.Error(w, "egress denied", status)
		return
	}
	address, err := ResolvePinned(request.Context(), g.Resolver, host)
	if err != nil {
		g.logDecision(host, port, "denied", "dns_policy", 0, started)
		http.Error(w, "egress denied", http.StatusForbidden)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxy transport unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	deadline := started.Add(time.Duration(g.Policy.Quota.MaxDurationSec) * time.Second)
	_ = client.SetDeadline(deadline)
	if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffered.Flush(); err != nil {
		return
	}
	hello, serverName, err := readClientHello(buffered.Reader)
	if err != nil || serverName != host {
		g.logDecision(host, port, "denied", "tls_sni_mismatch", 0, started)
		return
	}
	dial := g.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		dial = dialer.DialContext
	}
	upstream, err := dial(request.Context(), "tcp", net.JoinHostPort(address.String(), strconv.Itoa(int(port))))
	if err != nil {
		g.logDecision(host, port, "failed", "upstream_connect", 0, started)
		return
	}
	defer upstream.Close()
	_ = upstream.SetDeadline(deadline)
	connectionBytes := &atomic.Int64{}
	if _, err = (&quotaWriter{writer: upstream, total: &g.bytes, connection: connectionBytes, maximum: g.Policy.Quota.MaxBytes}).Write(hello); err != nil {
		g.logDecision(host, port, "denied", "byte_quota", connectionBytes.Load(), started)
		return
	}
	errorsChannel := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(&quotaWriter{writer: upstream, total: &g.bytes, connection: connectionBytes, maximum: g.Policy.Quota.MaxBytes}, buffered.Reader)
		errorsChannel <- copyErr
	}()
	go func() {
		_, copyErr := io.Copy(&quotaWriter{writer: client, total: &g.bytes, connection: connectionBytes, maximum: g.Policy.Quota.MaxBytes}, upstream)
		errorsChannel <- copyErr
	}()
	firstCopyErr := <-errorsChannel
	_ = client.Close()
	_ = upstream.Close()
	secondCopyErr := <-errorsChannel
	decision, reason := tunnelDecision(firstCopyErr, secondCopyErr)
	g.logDecision(host, port, decision, reason, connectionBytes.Load(), started)
}

func (g *Gateway) authorizeRequest(request *http.Request) (string, uint16, string, int) {
	if request.Method != http.MethodConnect {
		return "", 0, "tls_connect_required", http.StatusMethodNotAllowed
	}
	host, portText, err := net.SplitHostPort(strings.TrimSpace(request.Host))
	if err != nil {
		return "", 0, "invalid_destination", http.StatusBadRequest
	}
	host, err = normalizeFQDN(host)
	if err != nil {
		return "", 0, "literal_or_invalid_host", http.StatusForbidden
	}
	parsedPort, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || parsedPort == 0 {
		return host, 0, "invalid_port", http.StatusBadRequest
	}
	port := uint16(parsedPort)
	if !g.Policy.Allows(host, port, "tls") {
		return host, port, "not_allowlisted", http.StatusForbidden
	}
	if g.connections.Add(1) > int64(g.Policy.Quota.MaxConnections) {
		return host, port, "connection_quota", http.StatusTooManyRequests
	}
	return host, port, "", http.StatusOK
}

func (g *Gateway) logDecision(host string, port uint16, decision, reason string, bytes int64, started time.Time) {
	logger := g.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("sandbox egress", "run_id", g.RunID, "policy_digest", g.Policy.Digest,
		"fqdn", host, "port", port, "protocol", "tls", "decision", decision,
		"reason", reason, "bytes", bytes, "duration_ms", time.Since(started).Milliseconds())
}

var errByteQuota = errors.New("egress byte quota exceeded")

type quotaWriter struct {
	writer     io.Writer
	total      *atomic.Int64
	connection *atomic.Int64
	maximum    int64
}

func (w *quotaWriter) Write(payload []byte) (int, error) {
	length := int64(len(payload))
	for {
		current := w.total.Load()
		if length > w.maximum-current {
			return 0, errByteQuota
		}
		if w.total.CompareAndSwap(current, current+length) {
			break
		}
	}
	w.connection.Add(length)
	written, err := w.writer.Write(payload)
	if missing := len(payload) - written; missing > 0 {
		w.total.Add(-int64(missing))
		w.connection.Add(-int64(missing))
	}
	if err == nil && written != len(payload) {
		err = io.ErrShortWrite
	}
	return written, err
}

func (g *Gateway) Validate() error {
	if err := ValidateGatewayPolicy(g.Policy); err != nil {
		return fmt.Errorf("invalid egress policy: %w", err)
	}
	return nil
}

func tunnelDecision(copyErrors ...error) (string, string) {
	for _, copyErr := range copyErrors {
		if errors.Is(copyErr, errByteQuota) {
			return "denied", "byte_quota"
		}
	}
	for _, copyErr := range copyErrors {
		var networkError net.Error
		if errors.As(copyErr, &networkError) && networkError.Timeout() {
			return "denied", "duration_quota"
		}
	}
	return "allowed", "closed"
}
