package tools

import (
	"net/url"
	"strings"
	"sync"
)

// NetworkGrantBook holds mid-run egress grants approved by the user via Master escalation.
// Base allowlists stay on the tool; grants are consulted on every command check.
type NetworkGrantBook struct {
	mu      sync.RWMutex
	hosts   map[string]map[string]bool // runID → host
	remotes map[string]map[string]bool // runID → normalized remote
	quest   map[string]map[string]bool // questID → host (quest-scoped hosts)
	qRemote map[string]map[string]bool // questID → remote
}

func NewNetworkGrantBook() *NetworkGrantBook {
	return &NetworkGrantBook{
		hosts:   map[string]map[string]bool{},
		remotes: map[string]map[string]bool{},
		quest:   map[string]map[string]bool{},
		qRemote: map[string]map[string]bool{},
	}
}

func (b *NetworkGrantBook) GrantHostOnce(runID, host string) {
	if b == nil {
		return
	}
	host = normalizeHost(host)
	if runID == "" || host == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hosts[runID] == nil {
		b.hosts[runID] = map[string]bool{}
	}
	b.hosts[runID][host] = true
}

func (b *NetworkGrantBook) GrantHostQuest(questID, runID, host string) {
	if b == nil {
		return
	}
	host = normalizeHost(host)
	if host == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if questID != "" {
		if b.quest[questID] == nil {
			b.quest[questID] = map[string]bool{}
		}
		b.quest[questID][host] = true
	}
	if runID != "" {
		if b.hosts[runID] == nil {
			b.hosts[runID] = map[string]bool{}
		}
		b.hosts[runID][host] = true
	}
}

func (b *NetworkGrantBook) GrantRemoteOnce(runID, remote string) {
	if b == nil {
		return
	}
	remote = NormalizeGitRemote(remote)
	if runID == "" || remote == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remotes[runID] == nil {
		b.remotes[runID] = map[string]bool{}
	}
	b.remotes[runID][remote] = true
}

func (b *NetworkGrantBook) GrantRemoteQuest(questID, runID, remote string) {
	if b == nil {
		return
	}
	remote = NormalizeGitRemote(remote)
	if remote == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if questID != "" {
		if b.qRemote[questID] == nil {
			b.qRemote[questID] = map[string]bool{}
		}
		b.qRemote[questID][remote] = true
	}
	if runID != "" {
		if b.remotes[runID] == nil {
			b.remotes[runID] = map[string]bool{}
		}
		b.remotes[runID][remote] = true
	}
}

func (b *NetworkGrantBook) HostsFor(runID, questID string) []string {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	add := func(m map[string]bool) {
		for host := range m {
			if !seen[host] {
				seen[host] = true
				out = append(out, host)
			}
		}
	}
	add(b.hosts[runID])
	add(b.quest[questID])
	return out
}

func (b *NetworkGrantBook) RemotesFor(runID, questID string) []string {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	add := func(m map[string]bool) {
		for remote := range m {
			if !seen[remote] {
				seen[remote] = true
				out = append(out, remote)
			}
		}
	}
	add(b.remotes[runID])
	add(b.qRemote[questID])
	return out
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if parsed, err := url.Parse("https://" + host); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname())
	}
	return host
}

// NormalizeGitRemote produces a comparable form for confirmed remotes.
func NormalizeGitRemote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "git@") {
		// git@host:owner/repo(.git)
		rest := raw[4:]
		host, path, ok := strings.Cut(rest, ":")
		if !ok {
			return strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(raw, "/"), ".git"))
		}
		path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
		return strings.ToLower(host) + ":" + strings.ToLower(path)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(raw, "/"), ".git"))
	}
	path := strings.TrimSuffix(strings.TrimSuffix(parsed.EscapedPath(), "/"), ".git")
	return strings.ToLower(parsed.Hostname() + path)
}

func GitRemoteAllowed(candidate string, allowlist []string) bool {
	norm := NormalizeGitRemote(candidate)
	if norm == "" {
		return false
	}
	for _, allowed := range allowlist {
		if NormalizeGitRemote(allowed) == norm {
			return true
		}
	}
	return false
}
