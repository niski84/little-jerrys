package maintainer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func privateTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fixtureArtifact(t *testing.T, root, revision, content string) Manifest {
	t.Helper()
	dir := filepath.Join(root, "artifacts", revision)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "jerry")
	if err := os.WriteFile(binary, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	manifest, err := fileManifest(binary, revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}
func TestExecutorOneShotAuthReplayRecoveryAndRollback(t *testing.T) {
	root, install := privateTemp(t), privateTemp(t)
	old, newRevision := strings.Repeat("a", 40), strings.Repeat("b", 40)
	fixtureArtifact(t, root, old, "old fixture binary")
	fixtureArtifact(t, root, newRevision, "new fixture binary")
	binary := filepath.Join(install, "jerry")
	if err := os.WriteFile(binary, []byte("old fixture binary"), 0755); err != nil {
		t.Fatal(err)
	}
	var running atomic.Value
	running.Store(old)
	var restarts atomic.Int32
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(health{Status: "ok", Revision: running.Load().(string), Modified: "false", DeviceID: "fixture-device", AutoUpdate: "false"})
	}))
	defer healthServer.Close()
	options := Options{Root: root, Binary: binary, HealthURL: healthServer.URL + "/api/health", Target: "fixture-pi", DeviceID: "fixture-device", Token: strings.Repeat("t", 32), Restart: func(context.Context) error {
		restarts.Add(1)
		content, _ := os.ReadFile(binary)
		if string(content) == "new fixture binary" {
			running.Store(newRevision)
		} else {
			running.Store(old)
		}
		return nil
	}}
	e, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { e.Close() }()
	// Fake byte artifacts isolate state-machine behavior; actual build identity
	// verification has a separate real linux/arm64 binary smoke below.
	e.verify = func(string, string) error { return nil }
	if other, err := New(options); err == nil {
		other.Close()
		t.Fatal("second target owner accepted")
	}
	call := func(method, path string, body any, authorized bool) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		if authorized {
			req.Header.Set("Authorization", "Bearer "+options.Token)
		}
		out := httptest.NewRecorder()
		e.ServeHTTP(out, req)
		return out
	}
	req := Request{Schema: 1, OperationID: "11111111-1111-4111-8111-111111111111", Action: "deploy", Target: options.Target, DeviceID: options.DeviceID, Repository: "niski84/little-jerrys", Revision: newRevision, ExpectedRevision: old, Digest: strings.Repeat("c", 64)}
	if got := call("POST", "/api/maintainer/deploy", req, false); got.Code != 401 {
		t.Fatal("unauthenticated deploy")
	}
	bad := req
	bad.Repository = "other/repo"
	if got := call("POST", "/api/maintainer/deploy", bad, true); got.Code != 422 {
		t.Fatal("foreign repository")
	}
	artifactPath := filepath.Join(root, "artifacts", newRevision, "jerry")
	if err := os.WriteFile(artifactPath, []byte("tampered fixture binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := call("POST", "/api/maintainer/deploy", req, true); got.Code != 409 || restarts.Load() != 0 {
		t.Fatal("tampered artifact installed")
	}
	if err := os.WriteFile(artifactPath, []byte("new fixture binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := call("POST", "/api/maintainer/deploy", req, true); got.Code != 202 {
		t.Fatalf("approval: %d %s", got.Code, got.Body.String())
	}
	e.work.Wait()
	if restarts.Load() != 1 || running.Load().(string) != newRevision {
		t.Fatal("install did not run once")
	}
	// Throw away the POST acknowledgement and restart the executor. GET must
	// recover the installed identity, without replaying the recorded intent.
	e.Close()
	e, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	e.verify = func(string, string) error { return nil }
	got := call("GET", "/api/maintainer/operations/"+req.OperationID, nil, true)
	var status Operation
	json.Unmarshal(got.Body.Bytes(), &status)
	if got.Code != 200 || status.State != "installed" || restarts.Load() != 1 {
		t.Fatalf("recovery: %s", got.Body.String())
	}
	if got := call("POST", "/api/maintainer/deploy", req, true); got.Code != 200 {
		t.Fatal("identical replay not reconciled")
	}
	e.work.Wait()
	if restarts.Load() != 1 {
		t.Fatal("replay restarted target")
	}
	bad = req
	bad.Digest = strings.Repeat("d", 64)
	if got := call("POST", "/api/maintainer/deploy", bad, true); got.Code != 409 {
		t.Fatal("changed request reused operation")
	}
	rollback := req
	rollback.OperationID = "22222222-2222-4222-8222-222222222222"
	rollback.Action = "rollback"
	rollback.Revision, rollback.ExpectedRevision = old, newRevision
	if got := call("POST", "/api/maintainer/deploy", rollback, true); got.Code != 202 {
		t.Fatalf("rollback: %s", got.Body.String())
	}
	e.work.Wait()
	got = call("GET", "/api/maintainer/operations/"+rollback.OperationID, nil, true)
	json.Unmarshal(got.Body.Bytes(), &status)
	if status.State != "installed" || restarts.Load() != 2 || running.Load().(string) != old {
		t.Fatal("exact rollback failed")
	}
	if got := call("GET", "/api/maintainer/operations/33333333-3333-4333-8333-333333333333", nil, true); got.Code != 404 {
		t.Fatal("unknown operation not explicit")
	}
}

func TestInterruptedIntentDoesNotReplay(t *testing.T) {
	root, install := privateTemp(t), privateTemp(t)
	binary := filepath.Join(install, "jerry")
	os.WriteFile(binary, []byte("old"), 0755)
	old, next := strings.Repeat("a", 40), strings.Repeat("b", 40)
	fixtureArtifact(t, root, old, "old")
	fixtureArtifact(t, root, next, "new")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(health{Status: "ok", Revision: old, Modified: "false", DeviceID: "pi", AutoUpdate: "false"})
	}))
	defer server.Close()
	var starts atomic.Int32
	options := Options{Root: root, Binary: binary, HealthURL: server.URL + "/api/health", Target: "pi", DeviceID: "pi", Token: strings.Repeat("t", 32), Restart: func(context.Context) error { starts.Add(1); return nil }}
	e, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	op := Operation{Request: Request{Schema: 1, OperationID: "11111111-1111-4111-8111-111111111111", Action: "deploy", Target: "pi", DeviceID: "pi", Repository: "niski84/little-jerrys", Revision: next, ExpectedRevision: old, Digest: strings.Repeat("d", 64)}, State: "applying"}
	if err := e.save(op); err != nil {
		t.Fatal(err)
	}
	e.Close()
	e, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	req := httptest.NewRequest("GET", "/api/maintainer/operations/"+op.OperationID, nil)
	req.Header.Set("Authorization", "Bearer "+options.Token)
	out := httptest.NewRecorder()
	e.ServeHTTP(out, req)
	if starts.Load() != 0 || !strings.Contains(out.Body.String(), `"state":"applying"`) {
		t.Fatal("interrupted intent replayed or falsely completed")
	}
}

func TestStageRealCleanARM64BinaryAndRejectTampering(t *testing.T) {
	if testing.Short() {
		t.Skip("real Go cross-build smoke")
	}
	repo, output, root := privateTemp(t), privateTemp(t), privateTemp(t)
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "little-jerrys"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"go.mod": "module github.com/niski84/little-jerrys\n\ngo 1.25.0\n", "cmd/little-jerrys/main.go": "package main\nfunc main() {}\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(name string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=arm64", "GOTOOLCHAIN=local", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v %s", name, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("git", "init", "-q")
	run("git", "add", "go.mod", "cmd/little-jerrys/main.go")
	run("git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "clean fixture")
	revision := run("git", "rev-parse", "HEAD")
	binary := filepath.Join(output, "jerry")
	run("go", "build", "-buildvcs=true", "-trimpath", "-o", binary, "./cmd/little-jerrys")
	if err := os.Chmod(binary, 0755); err != nil {
		t.Fatal(err)
	}
	if err := StageArtifact(root, binary, revision); err != nil {
		t.Fatal(err)
	}
	if err := StageArtifact(root, binary, revision); err == nil {
		t.Fatal("immutable revision slot overwritten")
	}
	if err := StageArtifact(root, binary, strings.Repeat("a", 40)); err == nil {
		t.Fatal("wrong embedded revision accepted")
	}
	if err := os.WriteFile(filepath.Join(repo, "cmd/little-jerrys/main.go"), []byte("package main\nfunc main(){ println(\"dirty\") }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("go", "build", "-buildvcs=true", "-trimpath", "-o", binary, "./cmd/little-jerrys")
	if err := verifyBuild(binary, revision); err == nil {
		t.Fatal("dirty build accepted")
	}
	// Symlinks and group-writable artifact files never enter the actuator.
	if err := os.Chmod(binary, 0775); err != nil {
		t.Fatal(err)
	}
	if err := secureFile(binary, false); err == nil {
		t.Fatal("writable artifact accepted")
	}
	link := filepath.Join(output, "linked")
	os.Symlink(binary, link)
	if err := secureFile(link, false); err == nil {
		t.Fatal("symlink artifact accepted")
	}
	t.Log(fmt.Sprintf("Verified real clean linux/arm64 binary for fixture commit %.12s; dirty/wrong-revision builds rejected", revision))
}
