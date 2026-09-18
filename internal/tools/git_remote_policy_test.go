package tools

import "testing"

func TestDeniedUnconfirmedGitRemote(t *testing.T) {
	confirmed := []string{"https://github.com/acme/app.git"}
	if reason := deniedUnconfirmedGitRemoteReason(`git clone https://evil.example/x.git`, confirmed, nil); reason == "" {
		t.Fatal("expected deny for unconfirmed remote")
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git clone https://github.com/acme/app.git`, confirmed, nil); reason != "" {
		t.Fatalf("expected allow confirmed remote, got %q", reason)
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git clone https://github.com/acme/app`, confirmed, nil); reason != "" {
		t.Fatalf("expected allow normalized remote without .git, got %q", reason)
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git clone`, confirmed, nil); reason == "" {
		t.Fatal("expected deny clone without URL")
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git fetch`, nil, nil); reason == "" {
		t.Fatal("expected deny fetch without confirmed origin")
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git fetch`, confirmed, nil); reason != "" {
		t.Fatalf("expected allow fetch with confirmed origin, got %q", reason)
	}
	if reason := deniedUnconfirmedGitRemoteReason(`git status`, confirmed, nil); reason != "" {
		t.Fatalf("status must not hit git remote policy: %q", reason)
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
