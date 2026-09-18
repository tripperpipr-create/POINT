package egress

import (
	"crypto/tls"
	"net"
	"testing"
)

func TestReadClientHelloExtractsExactSNI(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		done <- tls.Client(client, &tls.Config{ServerName: "API.Example.com", MinVersion: tls.VersionTLS12}).Handshake()
		_ = client.Close()
	}()
	raw, name, err := readClientHello(server)
	_ = server.Close()
	<-done
	if err != nil || name != "api.example.com" || len(raw) < 100 {
		t.Fatalf("ClientHello: name=%q bytes=%d err=%v", name, len(raw), err)
	}
}

func TestReadClientHelloRequiresSNI(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- tls.Client(client, &tls.Config{ServerName: "8.8.8.8", MinVersion: tls.VersionTLS12}).Handshake()
		_ = client.Close()
	}()
	_, _, err := readClientHello(server)
	_ = server.Close()
	<-done
	if err == nil {
		t.Fatal("ClientHello without SNI was accepted")
	}
}
