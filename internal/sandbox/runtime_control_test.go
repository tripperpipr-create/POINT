package sandbox

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runtimeTestPack(t *testing.T) (string, RuntimeManifest, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	t.Setenv("XDG_CACHE_HOME", root)
	asset := []byte("pinned local artifact")
	hash := sha256.Sum256(asset)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	for _, name := range []string{"rootfs.tar", "image.tar"} {
		if err := os.WriteFile(filepath.Join(root, name), asset, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := RuntimeManifest{Schema: 1, Engine: "moby", EngineVersion: "28.0.0", License: "Apache-2.0", RootFS: RuntimeAsset{File: "rootfs.tar", SHA256: digest}, Image: RuntimeAsset{File: "image.tar", SHA256: digest}, ImageReference: "point-test:1", ImageDigest: digest}
	raw, _ := json.Marshal(m)
	path := filepath.Join(root, "runtime.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	_, identity, err := ReadRuntimeManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, m, identity
}

func TestRuntimePackRejectsTamperingAndPaths(t *testing.T) {
	path, m, _ := runtimeTestPack(t)
	if _, err := VerifyRuntimeAsset(path, m.Image); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), m.Image.File), []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRuntimeAsset(path, m.Image); err == nil {
		t.Fatal("corrupted image accepted")
	}
	for _, name := range []string{"../outside.tar", `C:\outside.tar`, "nested/rootfs.tar"} {
		m.RootFS.File = name
		raw, _ := json.Marshal(m)
		_ = os.WriteFile(path, raw, 0600)
		if _, _, err := ReadRuntimeManifest(path); err == nil {
			t.Fatalf("accepted asset %q", name)
		}
	}
}

func TestRuntimeExecutionLeasePreventsIdleStopAcrossWindows(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("cache environment override unsupported")
	}
	manifest, m, digest := runtimeTestPack(t)
	base, err := RuntimeControlPath(digest)
	if err != nil {
		t.Fatal(err)
	}
	owner := RuntimeOwner{Digest: digest, Distribution: RuntimeDistribution(m, digest), State: "ready", LastActivity: time.Now().Add(-time.Hour)}
	if err = atomicJSON(filepath.Join(base, "owner.json"), owner); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireRuntimeLease(context.Background(), manifest, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	owner.LastActivity = time.Now().Add(-time.Hour)
	if err = atomicJSON(filepath.Join(base, "owner.json"), owner); err != nil {
		t.Fatal(err)
	}
	stopped, err := StopIdleRuntime(context.Background(), manifest, digest, time.Now())
	if err != nil || stopped {
		t.Fatalf("active execution would be stopped: %v %v", stopped, err)
	}
	owner.Distribution = "foreign-project"
	if err = atomicJSON(filepath.Join(base, "owner.json"), owner); err != nil {
		t.Fatal(err)
	}
	if _, err = StopIdleRuntime(context.Background(), manifest, digest, time.Now()); err == nil {
		t.Fatal("foreign WSL ownership accepted")
	}
}

func TestRuntimeBuildStreamsOnlyManagedDockerfile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Dockerfile")
	if err := os.WriteFile(file, []byte("FROM sha256:pinned\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "private.txt"), []byte("must not leave Windows"), 0600); err != nil {
		t.Fatal(err)
	}
	args, archive, err := runtimeBuildContext([]string{"build", "--file", file, "--tag", "point-runtime-test", root})
	if err != nil {
		t.Fatal(err)
	}
	if args[2] != "Dockerfile" || args[len(args)-1] != "-" {
		t.Fatal(args)
	}
	reader := tar.NewReader(archive)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader)
	if header.Name != "Dockerfile" || !strings.Contains(string(data), "pinned") {
		t.Fatal("invalid managed build")
	}
	if _, err = reader.Next(); err != io.EOF {
		t.Fatal("extra file leaked into build")
	}
	if _, _, err = runtimeBuildContext([]string{"build", "--file", file, t.TempDir()}); err == nil {
		t.Fatal("Dockerfile outside context accepted")
	}
}
