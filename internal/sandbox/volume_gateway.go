package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/egress"
)

type gatewayLog struct {
	sync.Mutex
	data []byte
}

func (l *gatewayLog) Write(p []byte) (int, error) {
	l.Lock()
	defer l.Unlock()
	l.data = append(l.data, p...)
	if len(l.data) > 512*1024 {
		l.data = l.data[len(l.data)-512*1024:]
	}
	return len(p), nil
}

type warmGateway struct {
	Network, Name, Address string
	command                *exec.Cmd
	input                  io.WriteCloser
	decoder                *json.Decoder
	logs                   gatewayLog
	decisionLogs           string
}

func (b *ContainerBackend) startWarmGateway(ctx context.Context, s *volumeState) error {
	if s.Gateway != nil {
		return nil
	}
	helper, err := b.helperVolume(ctx, &s.Record)
	if err != nil {
		return err
	}
	g := &warmGateway{Network: "point-net-" + s.Record.ID, Name: "point-gateway-" + s.Record.ID}
	// A surviving idle stage can still hold the old internal network after a
	// core restart. Detach it before replacing gateway/network resources.
	for _, resource := range []struct{ name, kind string }{{"point-stage-" + s.Record.ID, "stage"}, {g.Name, "gateway"}} {
		exists, err := b.ownedResource(ctx, "container", resource.name, s.Record.ID, resource.kind)
		if err != nil {
			return err
		}
		if exists {
			if err := b.runInfrastructure(ctx, "rm", "--force", resource.name); err != nil {
				return err
			}
		}
	}
	s.Container = ""
	if exists, err := b.ownedResource(ctx, "network", g.Network, s.Record.ID, "network"); err != nil {
		return err
	} else if exists {
		if err := b.runInfrastructure(ctx, "network", "rm", g.Network); err != nil {
			return err
		}
	}
	args := append([]string{"network", "create", "--internal", "--driver", "bridge"}, b.labels(s.Record, "network")...)
	args = append(args, g.Network)
	if err = b.runInfrastructure(ctx, args...); err != nil {
		return err
	}
	args = []string{"run", "--detach", "--interactive", "--pull", "never", "--name", g.Name, "--network", g.Network, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--user", b.User, "--pids-limit", "64", "--memory", "128m", "--cpus", "0.25", "--ipc", "none", "--entrypoint", helperMount, "--mount", "type=volume,src=" + helper + ",dst=/point-tools,readonly"}
	args = append(args, b.labels(s.Record, "gateway")...)
	args = append(args, s.Record.BackendImageDigest, "run", "--gateway")
	if err = b.runInfrastructure(ctx, args...); err != nil {
		_ = b.runInfrastructure(ctx, "network", "rm", g.Network)
		return err
	}
	if err = b.runInfrastructure(ctx, "network", "connect", "bridge", g.Name); err != nil {
		b.removeWarmGateway(ctx, g)
		return err
	}
	g.Address, err = b.output(ctx, "inspect", g.Name, "--format", fmt.Sprintf(`{{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{end}}`, g.Network))
	if err != nil {
		b.removeWarmGateway(ctx, g)
		return err
	}
	g.Address = strings.TrimSpace(g.Address)
	g.command = b.commandFor(context.Background(), "attach", "--sig-proxy=false", g.Name)
	g.command.Env = dockerClientEnvironment()
	g.command.Stderr = &g.logs
	g.input, err = g.command.StdinPipe()
	if err != nil {
		b.removeWarmGateway(ctx, g)
		return err
	}
	output, err := g.command.StdoutPipe()
	if err != nil {
		b.removeWarmGateway(ctx, g)
		return err
	}
	g.decoder = json.NewDecoder(output)
	if err = g.command.Start(); err != nil {
		b.removeWarmGateway(ctx, g)
		return err
	}
	s.Gateway = g
	if s.Container != "" {
		_ = b.runInfrastructure(ctx, "rm", "--force", s.Container)
		s.Container = ""
	}
	return nil
}
func (g *warmGateway) control(ctx context.Context, action, operation, runID string, policy egress.Policy) error {
	request := map[string]any{"action": action, "operation": operation, "runId": runID, "policy": policy}
	type controlResult struct {
		err  error
		logs string
	}
	done := make(chan controlResult, 1)
	go func() {
		if err := json.NewEncoder(g.input).Encode(request); err != nil {
			done <- controlResult{err: err}
			return
		}
		var reply struct {
			Operation string `json:"operation"`
			Error     string `json:"error"`
			Logs      string `json:"logs"`
			Truncated bool   `json:"truncated"`
		}
		err := g.decoder.Decode(&reply)
		if err == nil && reply.Operation != operation {
			err = errors.New("gateway response identity differs")
		}
		if err == nil && reply.Error != "" {
			err = errors.New(reply.Error)
		}
		if err == nil && reply.Truncated {
			err = errors.New("gateway audit log exceeded its bound")
		}
		done <- controlResult{err: err, logs: reply.Logs}
	}()
	select {
	case result := <-done:
		if result.err == nil {
			g.decisionLogs = result.logs
		}
		return result.err
	case <-ctx.Done():
		_ = g.input.Close()
		return ctx.Err()
	}
}
func (b *ContainerBackend) removeWarmGateway(ctx context.Context, g *warmGateway) error {
	if g == nil {
		return nil
	}
	if g.input != nil {
		_ = g.input.Close()
	}
	if g.command != nil && g.command.Process != nil {
		_ = g.command.Process.Kill()
		_ = g.command.Wait()
	}
	id := strings.TrimPrefix(g.Name, "point-gateway-")
	var errs []error
	for _, r := range []struct{ kind, name, resource string }{{"container", g.Name, "gateway"}, {"network", g.Network, "network"}} {
		exists, err := b.ownedResource(ctx, r.kind, r.name, id, r.resource)
		if err == nil && exists {
			args := []string{r.kind, "rm"}
			if r.kind == "container" {
				args = append(args, "--force")
			}
			err = b.runInfrastructure(ctx, append(args, r.name)...)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func (b *ContainerBackend) endWarmGateway(s *volumeState, operation, runID string, policy egress.Policy) ([]EgressDecision, error) {
	if s.Gateway == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Gateway.control(ctx, "end", operation, runID, policy); err != nil {
		return nil, err
	}
	return parseEgressDecisions(s.Gateway.decisionLogs, policy.Digest, runID)
}
