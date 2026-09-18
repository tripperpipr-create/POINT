package egress

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGatewayAllowsPinnedTLSAndRejectsSNIMismatch(t *testing.T) {
	policy, err := Compile("ALLOWLIST", []string{"api.example.com:443"}, Quota{MaxConnections: 2, MaxBytes: 1024 * 1024, MaxDurationSec: 10})
	if err != nil {
		t.Fatal(err)
	}
	var dials atomic.Int64
	gateway := &Gateway{
		Policy: policy, Resolver: fixedResolver{addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			client, server := net.Pipe()
			go func() {
				buffer := make([]byte, 64*1024)
				_, _ = server.Read(buffer)
				_ = server.Close()
			}()
			return client, nil
		},
	}
	server := httptest.NewServer(gateway)
	defer server.Close()
	if err = connectTLS(server.Listener.Addr().String(), "api.example.com", "api.example.com"); err == nil {
		t.Fatal("fake upstream should close before completing TLS")
	}
	if dials.Load() != 1 {
		t.Fatalf("allowed TLS was not dialed: %d", dials.Load())
	}
	if err = connectTLS(server.Listener.Addr().String(), "api.example.com", "evil.example.com"); err == nil {
		t.Fatal("mismatched SNI should fail")
	}
	if dials.Load() != 1 {
		t.Fatalf("SNI mismatch reached upstream: %d", dials.Load())
	}
}

func TestGatewayRejectsLiteralIPAndConnectionQuota(t *testing.T) {
	policy, _ := Compile("ALLOWLIST", []string{"api.example.com:443"}, Quota{MaxConnections: 1, MaxBytes: 1024 * 1024, MaxDurationSec: 10})
	gateway := &Gateway{Policy: policy, Resolver: fixedResolver{addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	first := httptest.NewRecorder()
	missingDestination := httptest.NewRequest(http.MethodConnect, "http://proxy", nil)
	missingDestination.Host = ""
	gateway.ServeHTTP(first, missingDestination)
	if first.Code != http.StatusBadRequest {
		t.Fatalf("missing destination status=%d", first.Code)
	}
	literal := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodConnect, "http://proxy", nil)
	request.Host = "127.0.0.1:443"
	gateway.ServeHTTP(literal, request)
	if literal.Code != http.StatusForbidden {
		t.Fatalf("literal IP status=%d", literal.Code)
	}
	if _, _, reason, _ := gateway.authorizeRequest(connectRequest("api.example.com:443")); reason != "" {
		t.Fatal(reason)
	}
	if _, _, reason, status := gateway.authorizeRequest(connectRequest("api.example.com:443")); reason != "connection_quota" || status != http.StatusTooManyRequests {
		t.Fatalf("quota reason=%q status=%d", reason, status)
	}
}

func TestGatewayRejectsNonCanonicalPolicyAndEnforcesTransferQuotas(t *testing.T) {
	policy, err := Compile("ALLOWLIST", []string{"api.example.com:443"}, Quota{MaxConnections: 1, MaxBytes: 1024, MaxDurationSec: 10})
	if err != nil {
		t.Fatal(err)
	}
	policy.Rules[0].FQDN = "API.EXAMPLE.COM"
	policy.Digest = policyDigest(policy)
	if err = (&Gateway{Policy: policy}).Validate(); err == nil {
		t.Fatal("correctly hashed but non-canonical policy was accepted")
	}

	var destination bytes.Buffer
	total, connection := &atomic.Int64{}, &atomic.Int64{}
	writer := &quotaWriter{writer: &destination, total: total, connection: connection, maximum: 4}
	if _, err = writer.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("5")); !errors.Is(err, errByteQuota) {
		t.Fatalf("byte quota error=%v", err)
	}
	if decision, reason := tunnelDecision(nil, errByteQuota); decision != "denied" || reason != "byte_quota" {
		t.Fatalf("byte quota decision=%s reason=%s", decision, reason)
	}
	if decision, reason := tunnelDecision(timeoutTestError{}); decision != "denied" || reason != "duration_quota" {
		t.Fatalf("duration quota decision=%s reason=%s", decision, reason)
	}
}

type timeoutTestError struct{}

func (timeoutTestError) Error() string   { return "timeout" }
func (timeoutTestError) Timeout() bool   { return true }
func (timeoutTestError) Temporary() bool { return false }

func connectRequest(host string) *http.Request {
	request := httptest.NewRequest(http.MethodConnect, "http://proxy", nil)
	request.Host = host
	return request
}

func connectTLS(proxyAddress, connectHost, serverName string) error {
	connection, err := net.Dial("tcp", proxyAddress)
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err = io.WriteString(connection, "CONNECT "+connectHost+":443 HTTP/1.1\r\nHost: "+connectHost+":443\r\n\r\n"); err != nil {
		return err
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return &statusError{status: response.Status}
	}
	return tls.Client(connection, &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}).Handshake()
}

type statusError struct{ status string }

func (e *statusError) Error() string { return strings.TrimSpace(e.status) }
