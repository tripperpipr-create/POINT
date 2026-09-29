package tools

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"local-agent-workbench/internal/workspace"
)

func TestRunCommandOneCommandTLSGrant(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	requests := &capturingProcessExecutor{}
	book := NewNetworkGrantBook()
	tool := RunCommand{FS: fs, NetworkPolicy: "DENY", Grants: book, Executor: &controlledProcessExecutor{requests}, RunID: "run-1", QuestID: "quest-1"}
	command := json.RawMessage(`{"command":"curl https://packages.example.test:8443/module","reason":"download dependency"}`)
	checkDenied := func(wantTarget string) {
		t.Helper()
		result := tool.Execute(context.Background(), command)
		if result.OK || result.Error == nil || result.Error.Code != "network_denied" || result.Error.Target != wantTarget || result.Error.PolicyDigest == "" {
			t.Fatalf("network denial = %#v", result)
		}
	}
	checkDenied("packages.example.test:8443")
	if len(requests.requests) != 0 {
		t.Fatal("process prepared before grant")
	}
	book.GrantHostOnce("run-1", "packages.example.test:8443")
	result := tool.Execute(context.Background(), command)
	if !result.OK || len(requests.requests) != 1 || len(requests.requests[0].AllowedNetworkHosts) != 1 || requests.requests[0].AllowedNetworkHosts[0] != "packages.example.test:8443" {
		t.Fatalf("one-command grant failed: result=%#v requests=%#v", result, requests.requests)
	}
	var output struct {
		NetworkPolicyDigest string   `json:"networkPolicyDigest"`
		NetworkGrantScope   string   `json:"networkGrantScope"`
		NetworkTargets      []string `json:"networkTargets"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil || output.NetworkPolicyDigest == "" || output.NetworkGrantScope != "once" || len(output.NetworkTargets) != 1 || output.NetworkTargets[0] != "packages.example.test:8443" {
		t.Fatalf("missing persisted network attribution: output=%s error=%v", result.Output, err)
	}
	checkDenied("packages.example.test:8443")
}

func TestNetworkGrantScopesAndAtomicConsumption(t *testing.T) {
	book := NewNetworkGrantBook()
	book.GrantHostOnce("run-1", "repo.example.test:8443")
	if book.TakeHostOnce("run-2", "repo.example.test:8443") || book.TakeHostOnce("run-1", "repo.example.test:443") {
		t.Fatal("grant escaped its run or port")
	}
	var wg sync.WaitGroup
	wins := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wins <- book.TakeHostOnce("run-1", "repo.example.test:8443")
		}()
	}
	wg.Wait()
	close(wins)
	count := 0
	for won := range wins {
		if won {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("one-command grant won by %d calls", count)
	}
	book.GrantHostQuest("quest-1", "run-1", "repo.example.test:8443")
	if len(book.QuestHostsFor("quest-1")) != 1 || len(book.QuestHostsFor("quest-2")) != 0 {
		t.Fatal("quest grant escaped its quest")
	}
}

func TestRunCommandInvalidOrAmbiguousDestinationNeverCreatesAsk(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	requests := &capturingProcessExecutor{}
	tool := RunCommand{FS: fs, NetworkPolicy: "DENY", Executor: &controlledProcessExecutor{requests}, RunID: "run-1"}
	for _, command := range []string{"curl http://example.test/data", "curl https://127.0.0.1/data", "curl $TARGET"} {
		raw, _ := json.Marshal(map[string]string{"command": command, "reason": "test invalid destination"})
		result := tool.Execute(context.Background(), raw)
		if result.OK || result.Error == nil || result.Error.Code != "network_target_invalid" || result.Error.Target != "" {
			t.Fatalf("unsafe target %q: %#v", command, result)
		}
	}
	if len(requests.requests) != 0 {
		t.Fatal("unsafe network command reached process preparation")
	}
}
