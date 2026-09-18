package servers

import (
	"context"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildSSHArgsTerminatesOptionsBeforeDestination(t *testing.T) {
	profile := Profile{Host: "box.example", Port: 2222, User: "deploy", AuthMethod: AuthAgent}
	args := buildSSHArgs(profile, true, []string{"echo", "POINT_SSH_OK"})
	destination := slices.Index(args, profile.Destination())
	if destination < 1 || args[destination-1] != "--" {
		t.Fatalf("local option terminator must precede destination: %q", args)
	}
	if destination+1 >= len(args) || args[destination+1] != "echo" {
		t.Fatalf("remote command must begin immediately after destination: %q", args)
	}
	if slices.Contains(args[destination+1:], "--") {
		t.Fatalf("local option terminator leaked into remote command: %q", args)
	}
}

func TestOpenSSHRunnerHumanizesClosedPort(t *testing.T) {
	if _, err := LookPathSSH(); err != nil {
		t.Skip(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(strings.Split(listener.Addr().String(), ":")[1])
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = (OpenSSHRunner{}).Probe(ctx, Profile{
		Host: "127.0.0.1", Port: port, User: "point-e2e", AuthMethod: AuthAgent,
	}, "")
	if err == nil {
		t.Fatal("closed SSH port unexpectedly connected")
	}
	if strings.Contains(err.Error(), "exit 255") || !strings.Contains(err.Error(), "отклонил соединение") {
		t.Fatalf("SSH error was not humanized: %v", err)
	}
}

func TestRemoteListCommandQuotesPathForRemoteShell(t *testing.T) {
	path := "folder with spaces/'quoted';$(touch PWNED)"
	command := remoteListCommand(path)
	if !strings.HasPrefix(command, "LC_ALL=C ls -1Ap -- '") || !strings.HasSuffix(command, "'") {
		t.Fatalf("remote list command is not a single quoted argument: %q", command)
	}
	if strings.Contains(command, "-- folder with") || !strings.Contains(command, `'"'"'`) {
		t.Fatalf("remote shell quote did not preserve the path boundary: %q", command)
	}
}

func TestRemoteReadCommandIsBoundedAndQuotesPath(t *testing.T) {
	path := "folder with spaces/'quoted';$(touch PWNED)"
	command := remoteReadCommand(path)
	if !strings.HasPrefix(command, "head -c 65537 -- '") || !strings.HasSuffix(command, "'") {
		t.Fatalf("remote read command is not bounded and quoted: %q", command)
	}
	if !strings.Contains(command, `'"'"'`) {
		t.Fatalf("remote read command lost the quoted path boundary: %q", command)
	}
}

func TestCappedBufferDrainsWithoutGrowingPastLimit(t *testing.T) {
	buffer := cappedBuffer{limit: 5}
	if n, err := buffer.Write([]byte("abc")); err != nil || n != 3 {
		t.Fatalf("first write = %d, %v", n, err)
	}
	if n, err := buffer.Write([]byte("defgh")); err != nil || n != 5 {
		t.Fatalf("second write must report all bytes consumed: %d, %v", n, err)
	}
	if buffer.String() != "abcde" || !buffer.truncated {
		t.Fatalf("buffer did not enforce its cap: %q truncated=%v", buffer.String(), buffer.truncated)
	}
}
