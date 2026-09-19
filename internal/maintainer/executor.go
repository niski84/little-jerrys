package maintainer

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

var operationRE = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var targetRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

type Request struct {
	Schema           int    `json:"schema"`
	OperationID      string `json:"operation_id"`
	Action           string `json:"action"`
	Target           string `json:"target_id"`
	DeviceID         string `json:"device_id"`
	Repository       string `json:"repository"`
	Revision         string `json:"revision"`
	ExpectedRevision string `json:"expected_revision"`
	Digest           string `json:"candidate_digest"`
}
type Operation struct {
	Request
	State  string `json:"state"`
	Detail string `json:"detail"`
}
type Options struct {
	Root      string
	Binary    string
	HealthURL string
	Target    string
	DeviceID  string
	Token     string
	Restart   func(context.Context) error
}
type Executor struct {
	mu         sync.Mutex
	options    Options
	operations map[string]Operation
	lock       *os.File
	ctx        context.Context
	cancel     context.CancelFunc
	work       sync.WaitGroup
	verify     func(string, string) error
}
type health struct {
	Status     string `json:"status"`
	Revision   string `json:"revision"`
	Modified   string `json:"modified"`
	DeviceID   string `json:"device_id"`
	AutoUpdate string `json:"auto_update"`
}

func New(options Options) (*Executor, error) {
	u, err := url.Parse(options.HealthURL)
	if err != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/health" {
		return nil, fmt.Errorf("health must be an exact loopback HTTP health endpoint")
	}
	if !targetRE.MatchString(options.Target) || options.DeviceID == "" || len(options.DeviceID) > 128 || len(options.Token) < 32 || options.Restart == nil || !filepath.IsAbs(options.Root) || !filepath.IsAbs(options.Binary) {
		return nil, fmt.Errorf("invalid target, paths, restart function or credential")
	}
	if err := os.MkdirAll(options.Root, 0700); err != nil {
		return nil, err
	}
	for _, p := range []string{options.Root, filepath.Dir(options.Binary)} {
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil || resolved != p {
			return nil, fmt.Errorf("symlinked trusted directory refused")
		}
		if err := secureFile(p, true); err != nil {
			return nil, err
		}
	}
	lock, err := os.OpenFile(filepath.Join(options.Root, "executor.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another executor owns this target")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Executor{options: options, operations: map[string]Operation{}, lock: lock, ctx: ctx, cancel: cancel, verify: verifyBuild}
	fail := func(err error) (*Executor, error) { e.Close(); return nil, err }
	dir := filepath.Join(options.Root, "operations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fail(err)
	}
	if err := secureFile(dir, true); err != nil {
		return fail(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fail(err)
	}
	if len(entries) > 10000 {
		return fail(fmt.Errorf("operation audit capacity reached; operator archival required"))
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".intent-") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.Name() != id+".json" || !operationRE.MatchString(id) {
			return fail(fmt.Errorf("unrecognized operation journal entry"))
		}
		var op Operation
		if err := readJSONFile(filepath.Join(dir, entry.Name()), &op); err != nil {
			return fail(err)
		}
		if op.OperationID != id || e.validate(op.Request) != nil || (op.State != "accepted" && op.State != "applying" && op.State != "installed" && op.State != "failed") {
			return fail(fmt.Errorf("invalid retained operation"))
		}
		e.operations[id] = op
	}
	return e, nil
}
func (e *Executor) Close() {
	e.cancel()
	e.work.Wait()
	if e.lock != nil {
		syscall.Flock(int(e.lock.Fd()), syscall.LOCK_UN)
		e.lock.Close()
		e.lock = nil
	}
}
func readJSONFile(path string, dst any) error {
	if err := secureFile(path, false); err != nil {
		return err
	}
	if info, err := os.Stat(path); err != nil || info.Size() > 8192 {
		return fmt.Errorf("oversized or unreadable journal record")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 8193))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid trailing journal content")
	}
	return nil
}
func (e *Executor) save(op Operation) error {
	if err := atomicJSON(filepath.Join(e.options.Root, "operations", op.OperationID+".json"), op); err != nil {
		return err
	}
	e.operations[op.OperationID] = op
	return nil
}
func (e *Executor) validate(r Request) error {
	if r.Schema != 1 || !operationRE.MatchString(r.OperationID) || (r.Action != "deploy" && r.Action != "rollback") || r.Target != e.options.Target || r.DeviceID != e.options.DeviceID || r.Repository != "niski84/little-jerrys" || !revisionRE.MatchString(r.Revision) || !revisionRE.MatchString(r.ExpectedRevision) || r.Revision == r.ExpectedRevision || !digestRE.MatchString(r.Digest) {
		return fmt.Errorf("request outside registered target/revision contract")
	}
	return nil
}
func (e *Executor) health(ctx context.Context) (health, error) {
	var h health
	req, err := http.NewRequestWithContext(ctx, "GET", e.options.HealthURL, nil)
	if err != nil {
		return h, err
	}
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect refused") }}
	response, err := client.Do(req)
	if err != nil {
		return h, fmt.Errorf("health unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return h, fmt.Errorf("health not OK")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return h, fmt.Errorf("health oversized/incomplete")
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return h, err
	}
	if (h.Status != "ok" && h.Status != "healthy") || h.DeviceID != e.options.DeviceID || h.Modified != "false" || h.AutoUpdate != "false" {
		return h, fmt.Errorf("device must be healthy, clean and have legacy auto-update disabled")
	}
	return h, nil
}
func (e *Executor) artifact(revision string) (string, Manifest, error) {
	var m Manifest
	dir := filepath.Join(e.options.Root, "artifacts", revision)
	for _, p := range []string{filepath.Join(e.options.Root, "artifacts"), dir} {
		if err := secureFile(p, true); err != nil {
			return "", m, err
		}
	}
	if err := readJSONFile(filepath.Join(dir, "manifest.json"), &m); err != nil {
		return "", m, err
	}
	if m.Revision != revision || !digestRE.MatchString(m.SHA256) || m.Size < 1 || m.Size > maxBinary {
		return "", m, fmt.Errorf("invalid artifact manifest")
	}
	binary := filepath.Join(dir, "jerry")
	if err := secureFile(binary, false); err != nil {
		return "", m, err
	}
	if err := e.verify(binary, revision); err != nil {
		return "", m, err
	}
	actual, err := fileManifest(binary, revision)
	if err != nil || actual != m {
		return "", m, fmt.Errorf("artifact checksum mismatch")
	}
	return binary, m, nil
}
func (e *Executor) baseline(ctx context.Context, r Request) error {
	h, err := e.health(ctx)
	if err != nil {
		return err
	}
	if h.Revision != r.ExpectedRevision {
		return fmt.Errorf("installed revision changed")
	}
	if err := secureFile(e.options.Binary, false); err != nil {
		return err
	}
	if err := e.verify(e.options.Binary, r.ExpectedRevision); err != nil {
		return err
	}
	_, previous, err := e.artifact(r.ExpectedRevision)
	if err != nil {
		return fmt.Errorf("verified rollback artifact is required before installation")
	}
	installed, err := fileManifest(e.options.Binary, r.ExpectedRevision)
	if err != nil || installed != previous {
		return fmt.Errorf("installed binary differs from approved rollback artifact")
	}
	return nil
}
func (e *Executor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	want := sha256.Sum256([]byte("Bearer " + e.options.Token))
	got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	e.mu.Lock()
	defer e.mu.Unlock()
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/maintainer/operations/") {
		id := strings.TrimPrefix(r.URL.Path, "/api/maintainer/operations/")
		op, ok := e.operations[id]
		if !operationRE.MatchString(id) || !ok {
			http.Error(w, "unknown operation", 404)
			return
		}
		if op.State == "accepted" || op.State == "applying" {
			if h, err := e.health(r.Context()); err == nil && h.Revision == op.Revision {
				_, manifest, err := e.artifact(op.Revision)
				installed, hashErr := fileManifest(e.options.Binary, op.Revision)
				if err != nil || hashErr != nil || installed != manifest || e.verify(e.options.Binary, op.Revision) != nil {
					http.Error(w, "running identity and installed artifact disagree", 409)
					return
				}
				op.State = "installed"
				op.Detail = "Exact installed identity recovered; no operation replayed"
				if err := e.save(op); err != nil {
					http.Error(w, "journal unavailable", 503)
					return
				}
			}
		}
		json.NewEncoder(w).Encode(op)
		return
	}
	if r.Method != "POST" || r.URL.Path != "/api/maintainer/deploy" {
		http.NotFound(w, r)
		return
	}
	var in Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || e.validate(in) != nil {
		http.Error(w, "invalid exact-target request", 422)
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		http.Error(w, "trailing request data", 422)
		return
	}
	if prior, ok := e.operations[in.OperationID]; ok {
		if prior.Request != in {
			http.Error(w, "operation identity conflict", 409)
			return
		}
		json.NewEncoder(w).Encode(prior)
		return
	}
	if len(e.operations) >= 10000 {
		http.Error(w, "audit capacity reached", 409)
		return
	}
	for _, op := range e.operations {
		if op.State == "accepted" || op.State == "applying" {
			http.Error(w, "target has an unresolved operation; no replay", 409)
			return
		}
	}
	if in.Action == "rollback" {
		approved := false
		for _, prior := range e.operations {
			if prior.Action == "deploy" && prior.Revision == in.ExpectedRevision && prior.ExpectedRevision == in.Revision && prior.Digest == in.Digest && prior.DeviceID == in.DeviceID {
				approved = true
			}
		}
		if !approved {
			http.Error(w, "rollback must restore a recorded deployment baseline", 409)
			return
		}
	}
	if err := e.baseline(r.Context(), in); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if _, _, err := e.artifact(in.Revision); err != nil {
		http.Error(w, "exact staged artifact unavailable or invalid", 409)
		return
	}
	op := Operation{Request: in, State: "accepted", Detail: "Durable approval accepted for one exact local binary swap"}
	if err := e.save(op); err != nil {
		http.Error(w, "journal unavailable", 503)
		return
	}
	e.work.Add(1)
	go func() { defer e.work.Done(); e.apply(op) }()
	w.WriteHeader(202)
	json.NewEncoder(w).Encode(op)
}
func (e *Executor) apply(op Operation) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ctx, cancel := context.WithTimeout(e.ctx, 45*time.Second)
	defer cancel()
	fail := func(detail string) {
		op.State = "failed"
		op.Detail = detail + "; no automatic retry or rollback"
		_ = e.save(op)
	}
	if err := e.baseline(ctx, op.Request); err != nil {
		fail("Baseline changed before install")
		return
	}
	binary, manifest, err := e.artifact(op.Revision)
	if err != nil {
		fail("Artifact changed before install")
		return
	}
	op.State = "applying"
	op.Detail = "One-shot local swap intent saved; interrupted intent requires identity reconciliation"
	if err := e.save(op); err != nil {
		return
	}
	if ctx.Err() != nil {
		fail("Executor stopping before swap")
		return
	}
	if err := copyVerified(binary, e.options.Binary, manifest); err != nil {
		fail("Binary swap not acknowledged")
		return
	}
	if err := e.options.Restart(ctx); err != nil {
		fail("Service restart not acknowledged; inspect actual device state")
		return
	}
	// GET reconciles actual running revision. A successful systemctl return alone
	// is never presented as a healthy/installed application.
	op.Detail = "Swap/restart requested once; awaiting running revision identity"
	_ = e.save(op)
}
