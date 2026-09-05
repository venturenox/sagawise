//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"wtfsaga/internal/testx"
	"wtfsaga/utils"
)

// Contract §7: the process serves only when its configuration is valid and
// its stores are reachable; otherwise it exits non-zero. (#6, #8)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

func binary(t testx.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "sagawise-bin")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "sagawise")
		cmd := exec.Command("go", "build", "-o", binPath, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binPath
}

func freePort(t testx.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// lockedBuffer is an io.Writer the child process writes to from the copying
// goroutine while the test reads it; the mutex keeps the race detector quiet.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type proc struct {
	cmd     *exec.Cmd
	addr    string
	out     *lockedBuffer
	exited  chan struct{} // closed once the process has exited
	exitErr error         // valid after exited is closed
}

// launch starts the binary with the given DSL dir, services file and extra
// env, on a free port, against the local stores by default.
func launch(t testx.T, dslDir, servicesFile string, extra map[string]string) *proc {
	t.Helper()
	env := map[string]string{
		"REDIS_HOST": "localhost", "REDIS_PORT": "6379", "REDIS_CONNECTION_STRING": "",
		"POSTGRES_HOST": "localhost", "POSTGRES_PORT": "5432", "POSTGRES_USERNAME": "postgres",
		"POSTGRES_PASSWORD": "venturenox", "POSTGRES_DATABASE": "sagawise",
		"OTEL_SDK_DISABLED": "true",
		// Phase 8: the binary refuses to start without a key (see the
		// TestStartup_Auth* cases); the default launch has one.
		"SAGAWISE_API_KEYS": stKey,
	}
	for _, k := range []string{"REDIS_HOST", "REDIS_PORT", "POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USERNAME", "POSTGRES_PASSWORD", "POSTGRES_DATABASE"} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	for k, v := range extra {
		env[k] = v
	}
	p := &proc{addr: freePort(t), out: &lockedBuffer{}, exited: make(chan struct{})}
	env["SAGAWISE_ADDR"] = p.addr
	env["SAGAWISE_DSL_DIR"] = dslDir
	env["SAGAWISE_SERVICES_FILE"] = servicesFile

	p.cmd = exec.Command(binary(t))
	p.cmd.Env = os.Environ()
	for k, v := range env {
		p.cmd.Env = append(p.cmd.Env, k+"="+v)
	}
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	go func() { p.exitErr = p.cmd.Wait(); close(p.exited) }()
	t.Cleanup(func() {
		select {
		case <-p.exited:
		default:
			_ = p.cmd.Process.Kill()
			<-p.exited
		}
	})
	return p
}

// serving reports whether /live answers 200 within the timeout.
func (p *proc) serving(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-p.exited:
			return false
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+p.addr+"/live", nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// expectExit fails unless the process exits non-zero within the timeout.
func (p *proc) expectExit(t testx.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-p.exited:
		if p.exitErr == nil {
			t.Errorf("process exited 0; contract §7 wants a non-zero exit\n%s", tail(p.out))
		}
	case <-time.After(timeout):
		_ = p.cmd.Process.Kill()
		t.Errorf("process still running after %v; contract §7 wants a non-zero exit\n%s", timeout, tail(p.out))
	}
}

func tail(b *lockedBuffer) string {
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	return "  " + strings.Join(lines, "\n  ")
}

func writeDSL(t testx.T, wf utils.Workflow) string {
	t.Helper()
	dir := t.TempDir()
	data, _ := json.Marshal(utils.WorkflowData{Workflow: wf})
	if err := os.WriteFile(filepath.Join(dir, wf.Name+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeServices(t testx.T, services []utils.Service) string {
	t.Helper()
	data, _ := json.Marshal(services)
	path := filepath.Join(t.TempDir(), "services.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func goodWorkflow() utils.Workflow {
	return utils.Workflow{
		Name: "st_flow", Version: "1.0", Schema_version: "1.0",
		Tasks: []utils.Task{{Topic: "st_topic", From: "st_a", To: "st_b", Timeout: 1000}},
	}
}

// stKey is the API key the launched binary is configured with.
const stKey = "st-test-key"

// call sends one authenticated (or not) request to the running binary.
func (p *proc) call(t testx.T, method, path, auth string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, "http://"+p.addr+path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	_ = resp.Body.Close()
	return resp
}

func goodServices() []utils.Service {
	return []utils.Service{{ServiceName: "st_a", FailureUrl: "http://st_a/fail"}}
}

// Control: a valid configuration serves and shuts down cleanly on SIGINT.
func TestStartup_ValidConfigServes(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, writeDSL(t, goodWorkflow()), writeServices(t, goodServices()), nil)
		if !p.serving(15 * time.Second) {
			t.Fatalf("binary did not serve /live within 15s\n%s", tail(p.out))
		}
		_ = p.cmd.Process.Signal(syscall.SIGINT)
		select {
		case <-p.exited:
			if p.exitErr != nil {
				t.Errorf("exit after SIGINT: %v\n%s", p.exitErr, tail(p.out))
			}
		case <-time.After(10 * time.Second):
			t.Errorf("did not exit within 10s of SIGINT")
		}
	})
}

func TestStartup_MissingDSLDirExits(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, filepath.Join(t.TempDir(), "does-not-exist"), writeServices(t, goodServices()), nil)
		p.expectExit(t, 5*time.Second)
	})
}

func TestStartup_EmptyDSLDirExits(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, t.TempDir(), writeServices(t, goodServices()), nil)
		p.expectExit(t, 5*time.Second)
	})
}

func TestStartup_InvalidDSLExits(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		wf := goodWorkflow()
		wf.Tasks[0].Timeout = 0
		p := launch(t, writeDSL(t, wf), writeServices(t, goodServices()), nil)
		p.expectExit(t, 5*time.Second)
	})
}

func TestStartup_UnregisteredPublisherExits(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, writeDSL(t, goodWorkflow()), writeServices(t, nil), nil)
		p.expectExit(t, 5*time.Second)
	})
}

func TestStartup_PostgresUnreachableExits(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the 30s connect retry; skipped in -short")
	}
	testx.Run(t, func(t testx.T) {
		p := launch(t, writeDSL(t, goodWorkflow()), writeServices(t, goodServices()),
			map[string]string{"POSTGRES_HOST": "127.0.0.1", "POSTGRES_PORT": "1"})
		p.expectExit(t, 45*time.Second)
	})
}

func TestStartup_RedisUnreachableExits(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, writeDSL(t, goodWorkflow()), writeServices(t, goodServices()),
			map[string]string{"REDIS_HOST": "127.0.0.1", "REDIS_PORT": "1"})
		p.expectExit(t, 8*time.Second)
	})
}

// ---- Phase 8: authentication is on by default (docs/threat-model.md T1) ----

// No key configured and no explicit opt-out: the binary must not serve.
func TestStartup_NoAPIKeyExits(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, writeDSL(t, goodWorkflow()), writeServices(t, goodServices()), map[string]string{"SAGAWISE_API_KEYS": ""})
		p.expectExit(t, 5*time.Second)
	})
}

func TestStartup_BadAuthModeExits(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, writeDSL(t, goodWorkflow()), writeServices(t, goodServices()), map[string]string{"SAGAWISE_AUTH": "none"})
		p.expectExit(t, 5*time.Second)
	})
}

func TestStartup_WildcardCORSExits(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, writeDSL(t, goodWorkflow()), writeServices(t, goodServices()), map[string]string{"SAGAWISE_CORS_ORIGINS": "*"})
		p.expectExit(t, 5*time.Second)
	})
}

// With a key configured every endpoint but the probes demands it.
func TestStartup_APIKeyRequired(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, writeDSL(t, goodWorkflow()), writeServices(t, goodServices()), nil)
		if !p.serving(15 * time.Second) {
			t.Fatalf("binary did not serve /live within 15s\n%s", tail(p.out))
		}
		for _, probe := range []string{"/live", "/ready", "/health"} {
			if resp := p.call(t, http.MethodGet, probe, ""); resp.StatusCode != 200 {
				t.Errorf("%s without a key: %d, want 200 (probes are exempt)", probe, resp.StatusCode)
			}
		}
		if resp := p.call(t, http.MethodGet, "/workflows/list", ""); resp.StatusCode != 401 {
			t.Errorf("no key: %d, want 401", resp.StatusCode)
		}
		if resp := p.call(t, http.MethodGet, "/workflows/list", "Bearer wrong"); resp.StatusCode != 401 {
			t.Errorf("wrong key: %d, want 401", resp.StatusCode)
		}
		if resp := p.call(t, http.MethodPost, "/start_instance?workflow_name=nope", "Bearer "+stKey); resp.StatusCode != 404 {
			t.Errorf("right key: %d, want 404 (the request reached the engine)", resp.StatusCode)
		}
	})
}

// The explicit opt-out serves an open API (development only).
func TestStartup_AuthOffServesOpen(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		p := launch(t, writeDSL(t, goodWorkflow()), writeServices(t, goodServices()), map[string]string{"SAGAWISE_AUTH": "off", "SAGAWISE_API_KEYS": ""})
		if !p.serving(15 * time.Second) {
			t.Fatalf("binary did not serve /live within 15s\n%s", tail(p.out))
		}
		if resp := p.call(t, http.MethodGet, "/workflows/list", ""); resp.StatusCode != 200 {
			t.Errorf("no key with auth off: %d, want 200", resp.StatusCode)
		}
		if !strings.Contains(p.out.String(), "SAGAWISE_AUTH=off") {
			t.Errorf("no startup warning about the open API\n%s", tail(p.out))
		}
	})
}
