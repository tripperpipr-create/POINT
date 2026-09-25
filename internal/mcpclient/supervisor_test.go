package mcpclient

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorStartsOnceReusesAndReapsIdle(t *testing.T) {
	var resolves atomic.Int32
	s := NewSupervisor(func(context.Context, string) (Config, error) {
		resolves.Add(1)
		return fakeConfig(t, "normal"), nil
	}, SupervisorOptions{Idle: 150 * time.Millisecond, ReapEvery: 20 * time.Millisecond})
	defer s.StopAll()

	var first, second *Client
	if err := s.Call(context.Background(), "gl", func(c *Client) error { first = c; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.Call(context.Background(), "gl", func(c *Client) error { second = c; return nil }); err != nil {
		t.Fatal(err)
	}
	if first != second || resolves.Load() != 1 {
		t.Fatalf("server started %d times", resolves.Load())
	}
	if !s.Status("gl").Running {
		t.Fatal("status does not show a running server")
	}
	deadline := time.Now().Add(3 * time.Second)
	for s.Status("gl").Running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.Status("gl").Running {
		t.Fatal("idle server was not reaped")
	}
	select {
	case <-first.Done():
	case <-time.After(stopGrace + time.Second):
		t.Fatal("reaped server process kept running")
	}
	if err := s.Call(context.Background(), "gl", func(*Client) error { return nil }); err != nil || resolves.Load() != 2 {
		t.Fatalf("restart after reap: err=%v resolves=%d", err, resolves.Load())
	}
}

func TestSupervisorBacksOffThenStopsUntilReset(t *testing.T) {
	var dials atomic.Int32
	s := NewSupervisor(func(context.Context, string) (Config, error) { return Config{}, nil }, SupervisorOptions{
		BackoffMin:  10 * time.Millisecond,
		MaxFailures: 3,
		Dial: func(context.Context, Config) (*Client, error) {
			dials.Add(1)
			return nil, &Error{Kind: KindSpawn, Detail: "npx not found"}
		},
	})
	defer s.StopAll()
	for attempt := 1; attempt <= 3; attempt++ {
		err := s.Call(context.Background(), "gl", func(*Client) error { return nil })
		if KindOf(err) != KindSpawn {
			t.Fatalf("attempt %d: kind %q", attempt, KindOf(err))
		}
	}
	err := s.Call(context.Background(), "gl", func(*Client) error { return nil })
	if KindOf(err) != KindUnavailable || dials.Load() != 3 {
		t.Fatalf("after 3 failures: kind %q, dials %d", KindOf(err), dials.Load())
	}
	if status := s.Status("gl"); !status.Broken || status.Failures != 3 || KindOf(status.LastError) != KindSpawn {
		t.Fatalf("status = %+v", status)
	}
	s.Reset("gl")
	_ = s.Call(context.Background(), "gl", func(*Client) error { return nil })
	if dials.Load() != 4 {
		t.Fatalf("reset did not allow a new start: dials %d", dials.Load())
	}
}

// Недоверенный сервер и запертый секрет — не сбои сервера: они не должны
// выключать его до владельца.
func TestSupervisorConfigRefusalsAreNotFailures(t *testing.T) {
	s := NewSupervisor(func(context.Context, string) (Config, error) {
		return Config{}, &Error{Kind: KindNotTrusted, Detail: "command changed"}
	}, SupervisorOptions{MaxFailures: 2})
	defer s.StopAll()
	for attempt := 0; attempt < 5; attempt++ {
		if err := s.Call(context.Background(), "gl", func(*Client) error { return nil }); KindOf(err) != KindNotTrusted {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if status := s.Status("gl"); status.Broken || status.Failures != 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestSupervisorRestartsCrashedServerAndProbes(t *testing.T) {
	s := NewSupervisor(func(context.Context, string) (Config, error) {
		return fakeConfig(t, "normal"), nil
	}, SupervisorOptions{BackoffMin: 10 * time.Millisecond})
	defer s.StopAll()
	err := s.Call(context.Background(), "gl", func(c *Client) error {
		_, callErr := c.CallTool(context.Background(), "die", nil)
		return callErr
	})
	if KindOf(err) != KindExited {
		t.Fatalf("die: %v", err)
	}
	info, tools, err := s.Probe(context.Background(), "gl")
	if err != nil || info.Name != "fake" || len(tools) != 2 {
		t.Fatalf("probe after crash: %v %+v %d", err, info, len(tools))
	}
	if status := s.Status("gl"); status.Failures != 0 || !status.Running {
		t.Fatalf("probe did not reset failures: %+v", status)
	}
}

func TestSupervisorLogSurvivesStop(t *testing.T) {
	s := NewSupervisor(func(context.Context, string) (Config, error) {
		return fakeConfig(t, "banner"), nil
	}, SupervisorOptions{})
	defer s.StopAll()
	if err := s.Call(context.Background(), "gl", func(c *Client) error {
		_, listErr := c.ListTools(context.Background())
		return listErr
	}); err != nil {
		t.Fatal(err)
	}
	s.Stop("gl")
	if log := s.Log("gl"); log == "" {
		t.Fatal("log lost after stop")
	}
	s.Forget("gl")
	if s.Log("gl") != "" {
		t.Fatal("forgotten server kept its log")
	}
}
