package servers_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/servers"
)

type memStore struct {
	items map[string]servers.Profile
}

func (m *memStore) SaveServerProfile(_ context.Context, profile servers.Profile) error {
	if m.items == nil {
		m.items = map[string]servers.Profile{}
	}
	m.items[profile.ID] = profile
	return nil
}
func (m *memStore) ListServerProfiles(_ context.Context) ([]servers.Profile, error) {
	out := make([]servers.Profile, 0, len(m.items))
	for _, item := range m.items {
		out = append(out, item)
	}
	return out, nil
}
func (m *memStore) GetServerProfile(_ context.Context, id string) (servers.Profile, error) {
	item, ok := m.items[id]
	if !ok {
		return servers.Profile{}, fmt.Errorf("SSH-профиль %s не найден", id)
	}
	return item, nil
}
func (m *memStore) DeleteServerProfile(_ context.Context, id string) error {
	delete(m.items, id)
	return nil
}

type fakeRunner struct {
	probeErr error
	list     []string
	listErr  error
}

func (f fakeRunner) Probe(context.Context, servers.Profile, string) (servers.ProbeResult, error) {
	if f.probeErr != nil {
		return servers.ProbeResult{}, f.probeErr
	}
	return servers.ProbeResult{Message: "ok"}, nil
}
func (f fakeRunner) List(context.Context, servers.Profile, string, string) ([]string, string, error) {
	return f.list, strings.Join(f.list, "\n"), f.listErr
}
func (fakeRunner) Read(context.Context, servers.Profile, string, string) (string, bool, error) {
	return "preview", false, nil
}
func (fakeRunner) Exec(context.Context, servers.Profile, string, string, time.Duration) (string, string, int, error) {
	return "", "", 0, nil
}

func TestNormalizePrefersAgentAndRejectsBadHost(t *testing.T) {
	profile, err := servers.Normalize(servers.UpsertRequest{Host: "box.example", User: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.AuthMethod != servers.AuthAgent || profile.Port != 22 || profile.DisplayName != "deploy@box.example" {
		t.Fatalf("unexpected profile %#v", profile)
	}
	if _, err = servers.Normalize(servers.UpsertRequest{Host: "bad host", User: "u"}); err == nil {
		t.Fatal("expected host validation error")
	}
	if _, err = servers.Normalize(servers.UpsertRequest{Host: "box", User: "u", AuthMethod: servers.AuthKey}); err == nil {
		t.Fatal("expected key path required")
	}
}

func TestManagerProbeUpdatesStatus(t *testing.T) {
	store := &memStore{}
	manager := servers.Manager{Store: store, Runner: fakeRunner{}}
	saved, err := manager.Upsert(context.Background(), servers.UpsertRequest{Host: "srv.test", User: "root", AuthMethod: servers.AuthAgent})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Probe(context.Background(), saved.ID, "")
	if err != nil || !result.OK || result.Profile.Status != servers.StatusConnected {
		t.Fatalf("probe=%#v err=%v", result, err)
	}
	manager.Runner = fakeRunner{probeErr: context.DeadlineExceeded}
	result, err = manager.Probe(context.Background(), saved.ID, "")
	if err != nil || result.OK || result.Profile.Status != servers.StatusError {
		t.Fatalf("failed probe=%#v err=%v", result, err)
	}
}

func TestTerminalArgvRequiresSSHBinaryShape(t *testing.T) {
	profile := servers.Profile{Host: "h", Port: 2222, User: "u", AuthMethod: servers.AuthKey, PrivateKeyPath: `C:\keys\id_ed25519`}
	args, err := servers.TerminalArgv(profile)
	if err != nil {
		if !strings.Contains(err.Error(), "OpenSSH") {
			t.Fatalf("expected OpenSSH missing message, got %v", err)
		}
		return
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-p 2222") || !strings.Contains(joined, "u@h") || !strings.Contains(joined, "-i") {
		t.Fatalf("unexpected argv %v", args)
	}
}

func TestManagerReadsBoundedRemotePreview(t *testing.T) {
	store := &memStore{}
	manager := servers.Manager{Store: store, Runner: fakeRunner{}}
	saved, err := manager.Upsert(context.Background(), servers.UpsertRequest{Host: "srv.test", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.ReadRemote(context.Background(), saved.ID, "/srv/app/main.go", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "preview" || result.Path != "/srv/app/main.go" || result.Truncated {
		t.Fatalf("unexpected preview %#v", result)
	}
}
