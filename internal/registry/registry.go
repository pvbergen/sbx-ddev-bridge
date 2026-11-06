// Package registry stores the projects the bridge may act on, each bound to a token.
//
// Only a SHA-256 hash of each token is stored on the host. The plain token is
// written into the project (.ddev-bridge/token) for the sandbox client.
package registry

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ddev-bridge/internal/integrity"
)

// Dir is the bridge's own directory in a project (client, symlinks, token).
const Dir = ".ddev-bridge"

// TokenFile is the token location relative to a project root.
var TokenFile = filepath.Join(Dir, "token")

// Project is one registered ddev project.
type Project struct {
	Name      string `json:"name"`
	Root      string `json:"root"`
	TokenHash string `json:"token_sha256"`

	// Approved is the fingerprint of .ddev/ the developer approved on the host.
	// The bridge refuses to run while .ddev/ differs from it.
	Approved   integrity.Fingerprint `json:"ddev_approved,omitempty"`
	ApprovedAt string                `json:"ddev_approved_at,omitempty"`
}

// Registry is the on-disk list of projects.
type Registry struct {
	Projects []Project `json:"projects"`
}

// Path returns the registry file location. DDEV_BRIDGE_CONFIG_DIR overrides the directory.
func Path() (string, error) {
	dir := os.Getenv("DDEV_BRIDGE_CONFIG_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "ddev-bridge")
	}
	return filepath.Join(dir, "projects.json"), nil
}

// Load reads the registry; a missing file yields an empty registry.
func Load() (*Registry, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Registry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &r, nil
}

// Save writes the registry with 0600 permissions.
func (r *Registry) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Lookup returns the project bound to token, comparing in constant time.
func (r *Registry) Lookup(token string) (*Project, bool) {
	if token == "" {
		return nil, false
	}
	h := []byte(hashToken(token))
	var found *Project
	for i := range r.Projects {
		if subtle.ConstantTimeCompare(h, []byte(r.Projects[i].TokenHash)) == 1 {
			found = &r.Projects[i]
		}
	}
	return found, found != nil
}

// ValidateRoot checks that dir is a ddev project and returns its absolute path and name.
func ValidateRoot(dir string) (root, name string, err error) {
	root, err = filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	cfg := filepath.Join(root, ".ddev", "config.yaml")
	f, err := os.Open(cfg)
	if err != nil {
		return "", "", fmt.Errorf("%s is not a ddev project (no .ddev/config.yaml)", root)
	}
	defer f.Close()
	name = filepath.Base(root)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "name:") {
			if v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "name:")), `"'`); v != "" {
				name = v
			}
			break
		}
	}
	return root, name, nil
}

// Register adds (or re-keys) the project at dir and writes a fresh token into it.
func (r *Registry) Register(dir string) (*Project, error) {
	root, name, err := ValidateRoot(dir)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(buf)

	if err := writeToken(root, token); err != nil {
		return nil, err
	}

	fp, err := integrity.Snapshot(root)
	if err != nil {
		return nil, fmt.Errorf("fingerprint .ddev: %w", err)
	}
	p := Project{Name: name, Root: root, TokenHash: hashToken(token), Approved: fp, ApprovedAt: now()}
	for i := range r.Projects {
		if r.Projects[i].Root == root {
			r.Projects[i] = p
			return &r.Projects[i], r.Save()
		}
	}
	r.Projects = append(r.Projects, p)
	return &r.Projects[len(r.Projects)-1], r.Save()
}

// writeToken writes the token into the project through os.Root: the sandbox
// controls the project tree, so a symlinked .ddev-bridge/ or token must not
// redirect the write to a file elsewhere on the host.
func writeToken(root, token string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := r.MkdirAll(Dir, 0o755); err != nil {
		return fmt.Errorf("write token: %w (is %s a symlink out of the project?)", err, Dir)
	}
	if err := r.WriteFile(TokenFile, []byte(token+"\n"), 0o644); err != nil {
		return fmt.Errorf("write token: %w (is %s a symlink out of the project?)", err, TokenFile)
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// Approve records the current .ddev/ of the registered project at dir as
// approved and returns what changed since the previous approval.
func (r *Registry) Approve(dir string) (*Project, []string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		root = rr
	}
	for i := range r.Projects {
		if r.Projects[i].Root != root {
			continue
		}
		fp, err := integrity.Snapshot(root)
		if err != nil {
			return nil, nil, fmt.Errorf("fingerprint .ddev: %w", err)
		}
		diff := integrity.Diff(r.Projects[i].Approved, fp)
		r.Projects[i].Approved, r.Projects[i].ApprovedAt = fp, now()
		return &r.Projects[i], diff, r.Save()
	}
	return nil, nil, fmt.Errorf("%s is not registered (run `ddev-bridge register` first)", root)
}

// Unregister removes the project at dir.
func (r *Registry) Unregister(dir string) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		root = rr
	}
	out := r.Projects[:0]
	removed := false
	for _, p := range r.Projects {
		if p.Root == root {
			removed = true
			continue
		}
		out = append(out, p)
	}
	if !removed {
		return fmt.Errorf("%s is not registered", root)
	}
	r.Projects = out
	return r.Save()
}
