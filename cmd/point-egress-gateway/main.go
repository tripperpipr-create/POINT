package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"local-agent-workbench/internal/egress"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "point-egress-gateway:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: point-egress-gateway serve|probe")
	}
	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		listen := flags.String("listen", "0.0.0.0:8080", "proxy listen address")
		policyBase64 := flags.String("policy-base64", "", "base64 JSON egress policy")
		runID := flags.String("run-id", "", "bounded run attribution")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		policy, err := decodePolicy(*policyBase64)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
		gateway := &egress.Gateway{Policy: policy, Logger: logger, RunID: boundedID(*runID)}
		if err = gateway.Validate(); err != nil {
			return err
		}
		server := &http.Server{
			Addr: *listen, Handler: gateway, ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout: time.Duration(policy.Quota.MaxDurationSec) * time.Second,
		}
		done := make(chan error, 1)
		go func() { done <- server.ListenAndServe() }()
		select {
		case err = <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return server.Shutdown(shutdownCtx)
		}
	case "probe":
		flags := flag.NewFlagSet("probe", flag.ContinueOnError)
		address := flags.String("address", "127.0.0.1:8080", "gateway address")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		connection, err := net.DialTimeout("tcp", *address, time.Second)
		if err != nil {
			return err
		}
		return connection.Close()
	default:
		return errors.New("usage: point-egress-gateway serve|probe")
	}
}

func decodePolicy(value string) (egress.Policy, error) {
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return egress.Policy{}, err
	}
	var policy egress.Policy
	if err = json.Unmarshal(encoded, &policy); err != nil {
		return egress.Policy{}, err
	}
	return policy, nil
}

func boundedID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
