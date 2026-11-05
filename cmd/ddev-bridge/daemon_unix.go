//go:build unix

package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxLogBytes = 10 << 20 // rotate bridge.log to bridge.log.1 above this size on start

// stateDir holds the pid file and the log. DDEV_BRIDGE_STATE_DIR overrides it.
func stateDir() (string, error) {
	if d := os.Getenv("DDEV_BRIDGE_STATE_DIR"); d != "" {
		return d, os.MkdirAll(d, 0o700)
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	d := filepath.Join(base, "ddev-bridge")
	return d, os.MkdirAll(d, 0o700)
}

// health returns the version reported by a bridge listening on addr.
func health(addr string) (string, bool) {
	c := http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	s := strings.TrimSpace(string(b))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(s, "ok") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(s, "ok")), true
}

func readPid(dir string) int {
	b, _ := os.ReadFile(filepath.Join(dir, "bridge.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

// lock serialises ensure/stop (several projects may start at the same time).
func lock(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "ensure.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// ensure makes sure a bridge of this version is running on addr.
func ensure(addr string) error {
	dir, err := stateDir()
	if err != nil {
		return err
	}
	unlock, err := lock(dir)
	if err != nil {
		return err
	}
	defer unlock()

	if v, ok := health(addr); ok {
		if v == version {
			fmt.Printf("ddev-bridge %s is running on %s (pid %d)\n", v, addr, readPid(dir))
			return nil
		}
		if !alive(readPid(dir)) {
			return fmt.Errorf("a bridge (version %q) is running on %s but was not started by `ddev-bridge ensure`; stop it first", v, addr)
		}
		fmt.Printf("replacing ddev-bridge %s with %s\n", v, version)
		if err := stopLocked(dir, addr); err != nil {
			return err
		}
	}
	return start(dir, addr)
}

func start(dir, addr string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "bridge.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > maxLogBytes {
		os.Rename(logPath, logPath+".1")
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devnull.Close()

	cmd := exec.Command(exe, "serve", "--addr", addr)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		select {
		case err := <-exited:
			return fmt.Errorf("bridge exited during start (%v); see %s", err, logPath)
		case <-time.After(100 * time.Millisecond):
		}
		if v, ok := health(addr); ok && v == version {
			if err := os.WriteFile(filepath.Join(dir, "bridge.pid"), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
				return err
			}
			fmt.Printf("ddev-bridge %s started on %s (pid %d, log %s)\n", version, addr, pid, logPath)
			return nil
		}
	}
	syscall.Kill(pid, syscall.SIGTERM)
	return fmt.Errorf("bridge did not become healthy within 5s; see %s", logPath)
}

// stop terminates the bridge started by ensure.
func stop(addr string) error {
	dir, err := stateDir()
	if err != nil {
		return err
	}
	unlock, err := lock(dir)
	if err != nil {
		return err
	}
	defer unlock()
	if !alive(readPid(dir)) {
		os.Remove(filepath.Join(dir, "bridge.pid"))
		if _, ok := health(addr); ok {
			return errors.New("a bridge is running but was not started by `ddev-bridge ensure`; stop it where it was started")
		}
		fmt.Println("ddev-bridge is not running")
		return nil
	}
	if err := stopLocked(dir, addr); err != nil {
		return err
	}
	fmt.Println("ddev-bridge stopped")
	return nil
}

func stopLocked(dir, addr string) error {
	pid := readPid(dir)
	syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 50; i++ {
		_, up := health(addr)
		if !alive(pid) && !up {
			os.Remove(filepath.Join(dir, "bridge.pid"))
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	os.Remove(filepath.Join(dir, "bridge.pid"))
	return nil
}

// status prints whether the bridge is running.
func status(addr string) bool {
	dir, _ := stateDir()
	v, ok := health(addr)
	switch {
	case !ok:
		fmt.Printf("ddev-bridge is not running on %s (installed: %s)\n", addr, version)
	case v != version:
		fmt.Printf("ddev-bridge %s is running on %s (pid %d), but %s is installed; run `ddev-bridge ensure`\n", v, addr, readPid(dir), version)
	default:
		fmt.Printf("ddev-bridge %s is running on %s (pid %d)\n", v, addr, readPid(dir))
	}
	fmt.Printf("log: %s\n", filepath.Join(dir, "bridge.log"))
	return ok
}
