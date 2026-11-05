// Package integrity fingerprints a project's .ddev configuration.
//
// ddev reads hooks (including `exec-host`, which runs on the host), extra
// docker-compose files (which can mount any host directory) and custom
// commands (including host commands) from .ddev/, and `ddev add-on remove`
// runs the removal actions stored in .ddev/addon-metadata/<name>/manifest.yaml
// on the host. The agent can edit those files from the sandbox, so the bridge
// only runs commands while .ddev/ matches what the developer approved on the host.
package integrity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Fingerprint maps .ddev-relative paths to SHA-256 hashes of their content.
type Fingerprint map[string]string

// Directories under .ddev that hold data or bookkeeping, not configuration.
var skipDirs = map[string]bool{"db_snapshots": true, "import-db": true, "importdb": true}

// addonMetadata is ddev's add-on bookkeeping. Only the per-add-on manifest
// counts there: it holds the removal_actions ddev runs on the host.
const addonMetadata = "addon-metadata"

// addonMetadataSkip reports whether rel (slash-separated, under addon-metadata)
// is ignored: everything except addon-metadata/<name>/manifest.yaml.
func addonMetadataSkip(rel string, isDir bool) bool {
	parts := strings.Split(rel, "/")
	if isDir {
		return len(parts) > 2 // addon-metadata/<name>/<deeper>
	}
	return len(parts) != 3 || parts[2] != "manifest.yaml"
}

// Dot-entries that still matter: env files feed containers and compose
// interpolation, .global_commands may hold copies of host commands.
func keepDot(name string) bool {
	return strings.HasPrefix(name, ".env") || name == ".global_commands"
}

// Top-level directories whose "#ddev-generated" files ddev rewrites itself and
// that only configure the containers. Anywhere else (config*.yaml,
// docker-compose*.yaml, commands/host, mutagen, ...) the marker is ignored and
// the file is fingerprinted like any other, because it could be forged.
var regenerated = map[string]bool{
	"traefik": true, "nginx_full": true, "nginx": true, "apache": true, "xhprof": true,
	"web-build": true, "db-build": true, "homeadditions": true, "web-entrypoint.d": true,
}

func markerSkippable(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return false
	}
	if regenerated[parts[0]] {
		return true
	}
	// README/example files ddev drops into commands/<service>/, except host commands.
	if parts[0] == "commands" && len(parts) == 3 && parts[1] != "host" {
		n := parts[2]
		return strings.HasPrefix(n, "README") || strings.Contains(n, ".example")
	}
	return false
}

// Snapshot fingerprints <root>/.ddev. Skipped: dot-files except env files and
// .global_commands (ddev's generated state), data directories, add-on
// bookkeeping other than the manifests, and "#ddev-generated" files in
// directories ddev regenerates for the containers.
// Symlinks are recorded by their target.
func Snapshot(root string) (Fingerprint, error) {
	dir := filepath.Join(root, ".ddev")
	fp := Fingerprint{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		name := d.Name()
		if (strings.HasPrefix(name, ".") && !keepDot(name)) || (d.IsDir() && skipDirs[rel]) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if slash := filepath.ToSlash(rel); strings.HasPrefix(slash, addonMetadata+"/") && addonMetadataSkip(slash, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fp[filepath.ToSlash(rel)] = "symlink:" + target
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if markerSkippable(rel) && bytes.Contains(data, []byte("#ddev-generated")) {
			return nil
		}
		sum := sha256.Sum256(data)
		fp[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return fp, err
}

// Diff lists the paths that were added, removed or changed relative to approved.
func Diff(approved, current Fingerprint) []string {
	var out []string
	for p, h := range current {
		switch a, ok := approved[p]; {
		case !ok:
			out = append(out, "added: .ddev/"+p)
		case a != h:
			out = append(out, "changed: .ddev/"+p)
		}
	}
	for p := range approved {
		if _, ok := current[p]; !ok {
			out = append(out, "removed: .ddev/"+p)
		}
	}
	sort.Strings(out)
	return out
}

// AddonManifestHint explains a diff that only touches add-on manifests, which
// ddev writes after every `ddev add-on get` or `remove` on the host, after the
// add-on's own install actions have run. It returns "" for any other diff.
func AddonManifestHint(diff []string) string {
	if len(diff) == 0 {
		return ""
	}
	for _, d := range diff {
		_, path, _ := strings.Cut(d, ": ")
		parts := strings.Split(strings.TrimPrefix(path, ".ddev/"), "/")
		if !strings.HasPrefix(path, ".ddev/") || len(parts) != 3 || parts[0] != addonMetadata || parts[2] != "manifest.yaml" {
			return ""
		}
	}
	return "Only add-on manifests changed. That is expected after installing, updating or removing an add-on on the host " +
		"(including ddev-bridge itself); if nobody did, review the change before approving"
}
