package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease serves GitHub-like release URLs for tag: /latest redirects to
// /tag/<tag>, /download/<tag>/ serves a package whose install.sh writes
// "installed <tag>" to marker. A non-empty badSum replaces the checksum.
func fakeRelease(t *testing.T, tag, marker, badSum string) *httptest.Server {
	t.Helper()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	tw := tar.NewWriter(zw)
	script := "#!/bin/sh\necho installed " + tag + " > '" + marker + "'\n"
	tw.WriteHeader(&tar.Header{Name: "ddev-bridge/install.sh", Mode: 0o755, Size: int64(len(script))})
	tw.Write([]byte(script))
	tw.Close()
	zw.Close()
	sum := fmt.Sprintf("%x  ddev-bridge.tar.gz\n", sha256.Sum256(gz.Bytes()))
	if badSum != "" {
		sum = badSum
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/tag/", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/download/"+tag+"/ddev-bridge.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(gz.Bytes())
	})
	mux.HandleFunc("/download/"+tag+"/ddev-bridge.tar.gz.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sum))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// runUpdate runs `ddev bridge update args…` with an installed binary that
// reports version installed. It returns the output and what install.sh wrote.
func runUpdate(t *testing.T, srv *httptest.Server, marker, installed string, args ...string) (string, string, error) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	global := t.TempDir()
	bin := filepath.Join(global, distDir, "host", runtime.GOOS+"-"+runtime.GOARCH, "ddev-bridge")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("#!/bin/sh\n[ \"$1\" = version ] && echo "+installed+"\n"), 0o755)

	cmd := exec.Command("bash", append([]string{filepath.Join(repo, "commands", "host", "bridge"), "update"}, args...)...)
	cmd.Env = append(os.Environ(), "DDEV_GLOBAL_DIR="+global, "DDEV_BRIDGE_RELEASES="+srv.URL,
		"http_proxy=", "https_proxy=", "HTTP_PROXY=", "HTTPS_PROXY=", "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
	out, err := cmd.CombinedOutput()
	got, _ := os.ReadFile(marker)
	return string(out), strings.TrimSpace(string(got)), err
}

func TestUpdate(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	srv := fakeRelease(t, "v1.2.0", marker, "")

	// Latest is newer: downloads, checks and installs.
	out, got, err := runUpdate(t, srv, marker, "v1.1.0")
	if err != nil || got != "installed v1.2.0" {
		t.Fatalf("latest: err=%v marker=%q\n%s", err, got, out)
	}

	// Already at the latest: nothing to do.
	os.Remove(marker)
	out, got, err = runUpdate(t, srv, marker, "v1.2.0")
	if err != nil || got != "" || !strings.Contains(out, "already at v1.2.0") {
		t.Fatalf("up to date: err=%v marker=%q\n%s", err, got, out)
	}

	// An explicit tag reinstalls, even the installed version.
	out, got, err = runUpdate(t, srv, marker, "v1.2.0", "v1.2.0")
	if err != nil || got != "installed v1.2.0" {
		t.Fatalf("explicit tag: err=%v marker=%q\n%s", err, got, out)
	}

	// A tag without a release fails.
	os.Remove(marker)
	out, got, err = runUpdate(t, srv, marker, "v1.1.0", "v9.9.9")
	if err == nil || got != "" || !strings.Contains(out, "could not download") {
		t.Fatalf("missing release: err=%v marker=%q\n%s", err, got, out)
	}

	// A bad tag is refused before anything is fetched.
	out, _, err = runUpdate(t, srv, marker, "v1.1.0", "main;rm")
	if err == nil || !strings.Contains(out, "bad release tag") {
		t.Fatalf("bad tag: err=%v\n%s", err, out)
	}
}

func TestUpdateChecksumMismatch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	srv := fakeRelease(t, "v1.2.0", marker, strings.Repeat("0", 64)+"  ddev-bridge.tar.gz\n")
	out, got, err := runUpdate(t, srv, marker, "v1.1.0")
	if err == nil || got != "" || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("err=%v marker=%q\n%s", err, got, out)
	}
}
