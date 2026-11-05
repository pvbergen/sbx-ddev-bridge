//go:build unix

package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ddev-bridge/internal/proto"
	"ddev-bridge/internal/registry"
)

// fakeDdev behaves like ddev for the commands the tests use.
const fakeDdev = `#!/bin/sh
case "$1" in
  drush)  echo "cwd=$(pwd)"; echo "argv=$*"; echo "oops" >&2; exit 3 ;;
  exec)
    echo "argv=$*"
    case "$4" in
      touch) touch "$5" ;;
      sleep-touch) sleep 1; touch "$5" ;;
    esac ;;
  logs)   echo first; sleep 1; echo second ;;
  restart)
    sleep 300 &
    echo $! > "$PIDFILE"
    echo started
    wait ;;
esac
`

type env struct {
	srv   *httptest.Server
	root  string
	token string
	audit *bytes.Buffer
}

func setup(t *testing.T, timeout time.Duration) *env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DDEV_BRIDGE_CONFIG_DIR", filepath.Join(dir, "cfg"))
	t.Setenv("PIDFILE", filepath.Join(dir, "child.pid"))
	t.Setenv("DDEV_GLOBAL_DIR", filepath.Join(dir, "ddev-global"))

	bin := filepath.Join(dir, "ddev")
	if err := os.WriteFile(bin, []byte(fakeDdev), 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "proj")
	os.MkdirAll(filepath.Join(root, ".ddev"), 0o755)
	os.WriteFile(filepath.Join(root, ".ddev", "config.yaml"), []byte("name: demo\n"), 0o644)

	reg, _ := registry.Load()
	p, err := reg.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := os.ReadFile(filepath.Join(root, registry.TokenFile))

	s := &Server{Audit: &bytes.Buffer{}}
	s.Runner.Binary = bin
	s.Runner.Timeout = timeout
	e := &env{srv: httptest.NewServer(s.Handler()), root: p.Root, token: strings.TrimSpace(string(tok)), audit: s.Audit.(*bytes.Buffer)}
	t.Cleanup(e.srv.Close)
	return e
}

type result struct {
	stdout, stderr string
	exit           int
	denied         string
	frames         int
	firstFrameAt   time.Duration
}

func (e *env) run(t *testing.T, ctx context.Context, token string, argv ...string) (*result, int) {
	t.Helper()
	body, _ := json.Marshal(proto.RunRequest{Argv: argv})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.srv.URL+proto.RunPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, -1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	res := &result{exit: -1}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var f proto.Frame
		if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
			t.Fatalf("bad frame %q: %v", sc.Text(), err)
		}
		res.frames++
		if res.frames == 1 {
			res.firstFrameAt = time.Since(start)
		}
		switch {
		case f.Exit != nil:
			res.exit, res.denied = *f.Exit, f.Denied
		case f.Stream == proto.Stderr:
			res.stderr += string(f.Data)
		default:
			res.stdout += string(f.Data)
		}
	}
	return res, 200
}

func TestRunStreamsOutputAndExitCode(t *testing.T) {
	e := setup(t, time.Minute)
	res, status := e.run(t, context.Background(), e.token, "drush", "cr")
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	if res.exit != 3 || !strings.Contains(res.stdout, "cwd="+e.root) || !strings.Contains(res.stdout, "argv=drush cr") || res.stderr != "oops\n" {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestOutputIsStreamedLive(t *testing.T) {
	e := setup(t, time.Minute)
	res, _ := e.run(t, context.Background(), e.token, "logs")
	if res.stdout != "first\nsecond\n" || res.exit != 0 {
		t.Fatalf("unexpected result %+v", res)
	}
	if res.firstFrameAt > 700*time.Millisecond {
		t.Fatalf("first output arrived after %s; not streamed", res.firstFrameAt)
	}
}

func TestDenied(t *testing.T) {
	e := setup(t, time.Minute)
	res, _ := e.run(t, context.Background(), e.token, "delete", "-O")
	if res.exit != proto.ExitDenied || res.denied == "" || res.stdout != "" {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestDeniedWebAccess(t *testing.T) {
	e := setup(t, time.Minute)
	for _, argv := range [][]string{{"pull", "platform"}, {"drush", "@prod", "st"}, {"exec", "curl", "https://example.com"}} {
		res, _ := e.run(t, context.Background(), e.token, argv...)
		if res.exit != proto.ExitDenied || !strings.Contains(res.denied, "reaches the web") {
			t.Fatalf("%q: unexpected result %+v", argv, res)
		}
	}
	if !strings.Contains(e.audit.String(), `"outcome":"denied"`) {
		t.Fatalf("denial not audited: %s", e.audit)
	}
}

func TestDdevConfigChangePausesBridge(t *testing.T) {
	e := setup(t, time.Minute)
	cfg := filepath.Join(e.root, ".ddev", "config.yaml")
	os.WriteFile(cfg, []byte("name: demo\nhooks:\n  post-start:\n    - exec-host: curl evil\n"), 0o644)

	res, _ := e.run(t, context.Background(), e.token, "drush", "cr")
	if res.exit != proto.ExitDenied || !strings.Contains(res.denied, "changed: .ddev/config.yaml") || res.stdout != "" {
		t.Fatalf("unexpected result %+v", res)
	}

	reg, _ := registry.Load()
	if _, diff, err := reg.Approve(e.root); err != nil || len(diff) != 1 {
		t.Fatalf("approve: %v %v", diff, err)
	}
	res, _ = e.run(t, context.Background(), e.token, "drush", "cr")
	if res.exit != 3 {
		t.Fatalf("after approve: unexpected result %+v", res)
	}
}

func TestUnauthorized(t *testing.T) {
	e := setup(t, time.Minute)
	if _, status := e.run(t, context.Background(), "wrong", "drush", "cr"); status != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", status)
	}
}

func TestExecIsRewrittenToRaw(t *testing.T) {
	e := setup(t, time.Minute)
	res, _ := e.run(t, context.Background(), e.token, "exec", "phpunit", "--filter", "a;b")
	if !strings.Contains(res.stdout, "argv=exec --raw -- phpunit --filter a;b") {
		t.Fatalf("unexpected result %+v", res)
	}
	if !strings.Contains(e.audit.String(), `"ran":["exec","--raw","--","phpunit"`) {
		t.Fatalf("audit does not record rewritten argv: %s", e.audit)
	}
}

func waitGone(t *testing.T, pidFile string) {
	t.Helper()
	var pid int
	for i := 0; i < 50 && pid == 0; i++ {
		b, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("child pid never written")
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("grandchild %d still running after cancel", pid)
}

func TestCancelKillsProcessGroup(t *testing.T) {
	e := setup(t, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	e.run(t, ctx, e.token, "restart")
	waitGone(t, os.Getenv("PIDFILE"))
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	e := setup(t, 1*time.Second)
	res, _ := e.run(t, context.Background(), e.token, "restart")
	if res.exit != 124 || !strings.Contains(res.stderr, "timed out") {
		t.Fatalf("unexpected result %+v", res)
	}
	waitGone(t, os.Getenv("PIDFILE"))
}

func TestQueuedCommandIsRecheckedUnderLock(t *testing.T) {
	e := setup(t, time.Minute)
	first := make(chan *result)
	go func() {
		res, _ := e.run(t, context.Background(), e.token, "exec", "sleep-touch", ".ddev/extra.yaml")
		first <- res
	}()
	time.Sleep(300 * time.Millisecond) // the first command holds the lock and hasn't touched .ddev/ yet
	res, _ := e.run(t, context.Background(), e.token, "drush", "cr")
	if res.exit != proto.ExitDenied || !strings.Contains(res.denied, ".ddev/extra.yaml") {
		t.Fatalf("queued command ran despite the .ddev change: %+v", res)
	}
	if r := <-first; r.exit != 0 {
		t.Fatalf("first command: %+v", r)
	}
}
