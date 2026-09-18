package tools

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var gitRemoteMutatingPattern = regexp.MustCompile(`(?i)\bgit\s+(clone|remote\s+add|submodule\s+add)\b`)
var gitRemoteTrafficPattern = regexp.MustCompile(`(?i)\bgit\s+(clone|fetch|pull|push|remote\s+add|submodule\s+add)\b`)
var gitSSHURLPattern = regexp.MustCompile(`(?i)\bgit@[^\s"'` + "`" + `<>]+`)

// deniedUnconfirmedGitRemoteReason blocks git remotes that the user has not confirmed.
// Package registries stay on the host allowlist; git requires an explicit remote allowlist.
func deniedUnconfirmedGitRemoteReason(command string, confirmedRemotes, grantedRemotes []string) string {
	if !gitRemoteTrafficPattern.MatchString(command) {
		return ""
	}
	allow := append(append([]string{}, confirmedRemotes...), grantedRemotes...)
	targets := extractGitRemoteTargets(command)
	if gitRemoteMutatingPattern.MatchString(command) {
		if len(targets) == 0 {
			return "git clone/remote add requires an explicit confirmed repository URL; escalate to the Master so the user can approve it"
		}
		for _, target := range targets {
			if !GitRemoteAllowed(target, allow) {
				return fmt.Sprintf("git remote %q is not on the confirmed repository allowlist; escalate to the Master/user", target)
			}
		}
		return ""
	}
	// fetch/pull/push without a URL uses an already-configured origin: only allowed
	// when the quest already has at least one confirmed remote (intake/source).
	if len(targets) == 0 {
		if len(allow) == 0 {
			return "git fetch/pull against an unconfirmed origin is blocked; escalate to the Master/user"
		}
		return ""
	}
	for _, target := range targets {
		if !GitRemoteAllowed(target, allow) {
			return fmt.Sprintf("git remote %q is not on the confirmed repository allowlist; escalate to the Master/user", target)
		}
	}
	return ""
}

func extractGitRemoteTargets(command string) []string {
	var targets []string
	seen := map[string]bool{}
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		raw = strings.TrimRight(raw, ".,);]")
		if raw == "" || seen[raw] {
			return
		}
		seen[raw] = true
		targets = append(targets, raw)
	}
	for _, match := range networkURLPattern.FindAllString(command, -1) {
		add(match)
	}
	for _, match := range gitSSHURLPattern.FindAllString(command, -1) {
		add(match)
	}
	return targets
}

func extractNetworkHostTargets(command string) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, target := range networkURLPattern.FindAllString(command, -1) {
		host := hostFromNetworkTarget(target)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	return hosts
}

func hostFromNetworkTarget(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return normalizeHost(raw)
	}
	return normalizeHost(parsed.Hostname())
}
