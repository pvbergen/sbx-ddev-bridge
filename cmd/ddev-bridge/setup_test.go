package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ddev-bridge/internal/registry"
)

// ddevInstructions stands in for the commented examples ddev appends to
// global_config.yaml, which include a commented `# hooks:`.
const ddevInstructions = "router_http_port: \"80\"\n\n# hooks:\n#   post-start:\n#     - exec: \"echo hello\"\n"

// ddevMarshalled is our block after ddev rewrote the file (yaml.v3, 4 spaces).
const ddevMarshalled = "hooks:\n    post-start:\n        - exec-host: ddev bridge ensure\n"

func TestAddGlobalHook(t *testing.T) {
	out, changed, err := addGlobalHook(nil)
	if err != nil || !changed || string(out) != hookBlock {
		t.Fatalf("empty file: %q %v %v", out, changed, err)
	}

	out, changed, err = addGlobalHook([]byte(strings.TrimSuffix(ddevInstructions, "\n")))
	if err != nil || !changed || string(out) != ddevInstructions+"\n"+hookBlock {
		t.Fatalf("no trailing newline: %q %v %v", out, changed, err)
	}

	for _, have := range []string{
		ddevInstructions + "\n" + hookBlock,
		"a: b\n" + ddevMarshalled,
		"hooks:\n  post-start:\n    - exec-host: \"ddev bridge ensure\"\n",
		"hooks:\n  post-start:\n    - exec: x\n    - exec-host: ddev bridge ensure\n",
	} {
		if out, changed, err := addGlobalHook([]byte(have)); err != nil || changed || string(out) != have {
			t.Fatalf("already present %q: changed=%v err=%v", have, changed, err)
		}
	}

	for _, have := range []string{
		"hooks:\n  post-start:\n    - exec-host: echo hi\n",
		"hooks: {}\n",
	} {
		if _, _, err := addGlobalHook([]byte(have)); !errors.Is(err, errOtherHooks) {
			t.Fatalf("other hooks %q: want errOtherHooks, got %v", have, err)
		}
	}
}

func TestRemoveGlobalHook(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"as written by setup", ddevInstructions + "\n" + hookBlock, ddevInstructions},
		{"setup block only", hookBlock, ""},
		{"re-marshalled by ddev", "a: b\n" + ddevMarshalled + "z: y\n", "a: b\nz: y\n"},
		{"compact list", "hooks:\n  post-start:\n  - exec-host: ddev bridge ensure\nz: y\n", "z: y\n"},
		{"other task kept",
			"hooks:\n    post-start:\n        - exec: drush cr\n        - exec-host: ddev bridge ensure\n",
			"hooks:\n    post-start:\n        - exec: drush cr\n"},
		{"other hook kept",
			"hooks:\n    post-start:\n        - exec-host: ddev bridge ensure\n    pre-stop:\n        - exec-host: echo bye\n",
			"hooks:\n    pre-stop:\n        - exec-host: echo bye\n"},
	}
	for _, c := range cases {
		out, removed := removeGlobalHook([]byte(c.in))
		if !removed || string(out) != c.want {
			t.Errorf("%s: removed=%v\n got %q\nwant %q", c.name, removed, out, c.want)
		}
	}
	if _, removed := removeGlobalHook([]byte(ddevInstructions)); removed {
		t.Error("nothing to remove, but removed")
	}
}

// fakePackage is an unpacked tarball with the given client content.
func fakePackage(t *testing.T, client string) string {
	t.Helper()
	src := t.TempDir()
	files := map[string]string{
		filepath.Join(distDir, "host", runtime.GOOS+"-"+runtime.GOARCH, "ddev-bridge"):  "host",
		filepath.Join(distDir, "client", "linux-"+runtime.GOARCH, "ddev-bridge-client"): client,
		filepath.FromSlash(hostCommand): "#!/bin/sh\n",
	}
	for p, c := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(src, p)), 0o755)
		if err := os.WriteFile(filepath.Join(src, p), []byte(c), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func registeredProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".ddev"), 0o755)
	os.WriteFile(filepath.Join(dir, ".ddev", "config.yaml"), []byte("name: "+filepath.Base(dir)+"\n"), 0o644)
	reg, _ := registry.Load()
	if _, err := reg.Register(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSetup(t *testing.T) {
	t.Setenv("DDEV_BRIDGE_CONFIG_DIR", t.TempDir())
	global := t.TempDir()
	cfgPath := filepath.Join(global, globalCfg)
	os.WriteFile(cfgPath, []byte(ddevInstructions), 0o644)
	proj := registeredProject(t)
	clientPath := filepath.Join(proj, BinDir, "ddev-bridge-client")

	var out bytes.Buffer
	o := setupOpts{src: fakePackage(t, "v1"), globalDir: global, noSbx: true}
	if err := runSetup(o, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(clientPath); string(b) != "v1" {
		t.Fatalf("client not installed: %q", b)
	}
	if st, err := os.Stat(filepath.Join(global, filepath.FromSlash(hostCommand))); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("host command: %v %v", st, err)
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != ddevInstructions+"\n"+hookBlock {
		t.Fatalf("global config: %q", b)
	}
	token, _ := os.ReadFile(filepath.Join(proj, registry.TokenFile))

	// Update: the dist and the client are replaced, hook and token kept.
	out.Reset()
	o.src = fakePackage(t, "v2")
	if err := runSetup(o, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(global, distDir, "client", "linux-"+runtime.GOARCH, "ddev-bridge-client")); string(b) != "v2" {
		t.Fatalf("dist not replaced: %q", b)
	}
	if b, _ := os.ReadFile(clientPath); string(b) != "v2" {
		t.Fatalf("client not refreshed: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(proj, registry.TokenFile)); !bytes.Equal(b, token) {
		t.Fatal("setup must not change the token")
	}
	if b, _ := os.ReadFile(cfgPath); strings.Count(string(b), hookTask) != 1 {
		t.Fatalf("hook duplicated: %q", b)
	}
	if entries, _ := filepath.Glob(filepath.Join(global, distDir+".*")); len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestSetupStopsOnOtherHooks(t *testing.T) {
	t.Setenv("DDEV_BRIDGE_CONFIG_DIR", t.TempDir())
	global := t.TempDir()
	cfg := "hooks:\n  post-start:\n    - exec-host: echo hi\n"
	os.WriteFile(filepath.Join(global, globalCfg), []byte(cfg), 0o644)
	err := runSetup(setupOpts{src: fakePackage(t, "v1"), globalDir: global, noSbx: true}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "exec-host: "+hookTask) {
		t.Fatalf("want an error with the snippet, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(global, distDir)); !os.IsNotExist(err) {
		t.Fatal("nothing may be installed when the hook can't be added")
	}
}

func TestSetupReportsBadProjects(t *testing.T) {
	t.Setenv("DDEV_BRIDGE_CONFIG_DIR", t.TempDir())
	good := registeredProject(t)
	bad := registeredProject(t)
	gone := registeredProject(t)
	outside := t.TempDir()
	os.RemoveAll(filepath.Join(bad, BinDir))
	os.Symlink(outside, filepath.Join(bad, BinDir))
	os.RemoveAll(gone)

	var out bytes.Buffer
	err := runSetup(setupOpts{src: fakePackage(t, "v1"), globalDir: t.TempDir(), noSbx: true}, &out)
	if err == nil || !strings.Contains(err.Error(), "1 project(s)") {
		t.Fatalf("want one failed project, got %v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(good, BinDir, "ddev-bridge-client")); string(b) != "v1" {
		t.Fatal("the good project must still be updated")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside the project: %v", entries)
	}
	if !strings.Contains(out.String(), "is gone") {
		t.Fatalf("gone project not reported:\n%s", out.String())
	}
}

func TestUninstall(t *testing.T) {
	t.Setenv("DDEV_BRIDGE_CONFIG_DIR", t.TempDir())
	global := t.TempDir()
	cfgPath := filepath.Join(global, globalCfg)
	os.WriteFile(cfgPath, []byte(ddevInstructions), 0o644)
	proj := registeredProject(t)
	if err := runSetup(setupOpts{src: fakePackage(t, "v1"), globalDir: global, noSbx: true}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	// Refused while a project is enabled; nothing removed.
	err := runUninstall(global, defaultAddr, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), proj) {
		t.Fatalf("want a refusal naming the project, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(global, distDir)); err != nil {
		t.Fatal("refused uninstall must not remove anything")
	}

	reg, _ := registry.Load()
	reg.Unregister(proj)
	// ddev rewrote the config meanwhile: comment gone, re-indented.
	os.WriteFile(cfgPath, []byte("router_http_port: \"80\"\n"+ddevMarshalled), 0o644)
	if err := runUninstall(global, defaultAddr, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != "router_http_port: \"80\"\n" {
		t.Fatalf("hook not removed: %q", b)
	}
	for _, p := range []string{distDir, filepath.FromSlash(hostCommand)} {
		if _, err := os.Stat(filepath.Join(global, p)); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", p)
		}
	}
	// Running it again is fine.
	if err := runUninstall(global, defaultAddr, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}
