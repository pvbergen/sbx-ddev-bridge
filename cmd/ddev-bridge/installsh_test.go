package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runInstallSh runs install.sh from a fake package with a fake ddev that
// reports ddevVersion. The fake host binary records its arguments.
func runInstallSh(t *testing.T, ddevVersion string) (string, string, error) {
	t.Helper()
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	pkg := t.TempDir()
	script, err := os.ReadFile(filepath.Join(repo, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(pkg, "install.sh"), script, 0o755)

	arch := runtime.GOARCH
	host := filepath.Join(pkg, distDir, "host", runtime.GOOS+"-"+arch, "ddev-bridge")
	os.MkdirAll(filepath.Dir(host), 0o755)
	argsFile := filepath.Join(t.TempDir(), "args")
	os.WriteFile(host, []byte("#!/bin/sh\necho \"$@\" > '"+argsFile+"'\n"), 0o755)

	bin := t.TempDir()
	// Single-line JSON, as `ddev version -j` prints it.
	fake := `#!/bin/sh
if [ "${1:-}" = version ] && [ "${2:-}" = -j ]; then
  echo '{"level":"info","msg":"x","raw":{"DDEV version":"` + ddevVersion + `","global-ddev-dir":"/home/u/My Ddev"}}'
fi
`
	os.WriteFile(filepath.Join(bin, "ddev"), []byte(fake), 0o755)

	cmd := exec.Command("sh", filepath.Join(pkg, "install.sh"), "--no-sbx")
	cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
	out, err := cmd.CombinedOutput()
	args, _ := os.ReadFile(argsFile)
	return string(out), strings.TrimSpace(string(args)), err
}

func TestInstallSh(t *testing.T) {
	for _, v := range []string{"v1.25.5", "v1.26.0", "v2.0.0-rc1", "v1.25.10"} {
		out, args, err := runInstallSh(t, v)
		if err != nil {
			t.Fatalf("%s: %v\n%s", v, err, out)
		}
		if args != "setup --global-dir /home/u/My Ddev --no-sbx" {
			t.Fatalf("%s: host binary got %q", v, args)
		}
	}
	for _, v := range []string{"v1.25.4", "v1.24.10", "v0.9.0"} {
		out, args, err := runInstallSh(t, v)
		if err == nil || !strings.Contains(out, "too old") || args != "" {
			t.Fatalf("%s: want 'too old' and no setup, got err=%v args=%q\n%s", v, err, args, out)
		}
	}
}
