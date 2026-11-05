// Package server implements the host-side HTTP API of ddev-bridge.
package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ddev-bridge/internal/integrity"
	"ddev-bridge/internal/policy"
	"ddev-bridge/internal/proto"
	"ddev-bridge/internal/registry"
	"ddev-bridge/internal/runner"
)

// Server handles POST proto.RunPath.
type Server struct {
	Runner  runner.Options
	Audit   io.Writer // JSONL audit lines; defaults to os.Stderr
	Version string    // reported by /healthz

	mu    sync.Mutex
	locks map[string]*sync.Mutex // one in-flight command per project
	drush map[string]*drushList  // drush commands per project
}

func (s *Server) projectLock(root string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	if s.locks[root] == nil {
		s.locks[root] = &sync.Mutex{}
	}
	return s.locks[root]
}

type auditEntry struct {
	Time        string   `json:"time"`
	Remote      string   `json:"remote"`
	Project     string   `json:"project,omitempty"`
	Argv        []string `json:"argv,omitempty"`
	Ran         []string `json:"ran,omitempty"`          // argv actually executed, when rewritten by policy
	DdevChanged []string `json:"ddev_changed,omitempty"` // .ddev/ files the command left unapproved
	Outcome     string   `json:"outcome"`
	Reason      string   `json:"reason,omitempty"`
	ExitCode    *int     `json:"exit_code,omitempty"`
	Duration    string   `json:"duration,omitempty"`
}

func (s *Server) audit(e auditEntry) {
	e.Time = time.Now().UTC().Format(time.RFC3339)
	w := s.Audit
	if w == nil {
		w = os.Stderr
	}
	b, _ := json.Marshal(e)
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Write(append(b, '\n'))
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(proto.RunPath, s.handleRun)
	mux.HandleFunc("/v1/run", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "this bridge speaks "+proto.RunPath+"; update the ddev-bridge client in .ddev-bridge/bin", http.StatusGone)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok "+s.Version+"\n") })
	return mux
}

// frameWriter writes NDJSON frames and flushes after each one.
type frameWriter struct {
	enc   *json.Encoder
	flush func()
}

func newFrameWriter(w http.ResponseWriter) *frameWriter {
	w.Header().Set("Content-Type", proto.ContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fw := &frameWriter{enc: json.NewEncoder(w), flush: func() {}}
	if f, ok := w.(http.Flusher); ok {
		fw.flush = f.Flush
	}
	fw.flush()
	return fw
}

func (f *frameWriter) frame(fr proto.Frame) error {
	if err := f.enc.Encode(fr); err != nil {
		return err
	}
	f.flush()
	return nil
}

// streamWriter turns writes into output frames for one stream.
type streamWriter struct {
	fw     *frameWriter
	stream string
}

func (s streamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.fw.frame(proto.Frame{Stream: s.stream, Data: p}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// check runs the .ddev approval check and then the policy.
// fetch allows listing drush commands; the early check passes false.
func (s *Server) check(ctx context.Context, proj *registry.Project, argv []string, fetch bool) policy.Decision {
	if proj.Approved == nil {
		return policy.Decision{Reason: "the .ddev configuration of this project was never approved; ask the developer to run `ddev bridge approve` on the host"}
	}
	fp, err := integrity.Snapshot(proj.Root)
	if err != nil {
		return policy.Decision{Reason: "cannot read .ddev configuration: " + err.Error()}
	}
	if diff := integrity.Diff(proj.Approved, fp); len(diff) > 0 {
		hint := integrity.AddonManifestHint(diff)
		if hint != "" {
			hint += ". "
		}
		if len(diff) > 10 {
			diff = append(diff[:10], "…")
		}
		return policy.Decision{Reason: "the .ddev configuration changed since the developer approved it (" + strings.Join(diff, ", ") +
			"). ddev reads hooks, compose files and custom commands from there, so the bridge is paused for this project. " + hint +
			"Ask the developer to review the change and run `ddev bridge approve` on the host"}
	}
	p := policy.Project{
		Name:           proj.Name,
		Root:           proj.Root,
		OtherProjects:  policy.OtherProjects(proj.Name),
		CustomCommands: policy.CustomCommands(proj.Root),
		Drush:          s.drushLister(ctx, proj.Root, fetch),
	}
	return policy.Check(p, argv)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	entry := auditEntry{Remote: r.RemoteAddr}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	reg, err := registry.Load() // reload each time so `register` takes effect without restart
	if err != nil {
		log.Printf("registry: %v", err)
		http.Error(w, "registry unavailable", http.StatusInternalServerError)
		return
	}
	proj, ok := reg.Lookup(token)
	if !ok {
		entry.Outcome = "unauthorized"
		s.audit(entry)
		http.Error(w, "invalid or missing token", http.StatusUnauthorized)
		return
	}
	entry.Project = proj.Name

	var req proto.RunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		entry.Outcome = "bad_request"
		s.audit(entry)
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	entry.Argv = req.Argv

	if _, err := os.Stat(filepath.Join(proj.Root, ".ddev", "config.yaml")); err != nil {
		entry.Outcome, entry.Reason = "error", "project root no longer a ddev project"
		s.audit(entry)
		http.Error(w, "registered project root is no longer a ddev project", http.StatusConflict)
		return
	}

	fw := newFrameWriter(w)
	denied := func(reason string) {
		code := proto.ExitDenied
		entry.Outcome, entry.Reason, entry.ExitCode = "denied", reason, &code
		s.audit(entry)
		fw.frame(proto.Frame{Exit: &code, Denied: reason})
	}
	// Check before waiting for the lock, so denials don't queue behind a running command.
	if d := s.check(r.Context(), proj, req.Argv, false); !d.Allowed {
		denied(d.Reason)
		return
	}

	lock := s.projectLock(proj.Root)
	lock.Lock()
	defer lock.Unlock()
	if r.Context().Err() != nil { // client gave up while waiting for the lock
		entry.Outcome = "cancelled"
		s.audit(entry)
		return
	}
	// Check again: the command that held the lock may have changed .ddev/, or
	// the developer may have approved meanwhile, so reload the registry.
	if reg, err := registry.Load(); err == nil {
		if p, ok := reg.Lookup(token); ok {
			proj = p
		}
	}
	d := s.check(r.Context(), proj, req.Argv, true)
	if !d.Allowed {
		denied(d.Reason)
		return
	}
	if strings.Join(d.Argv, "\x00") != strings.Join(req.Argv, "\x00") {
		entry.Ran = d.Argv
	}

	start := time.Now()
	code := runner.Run(r.Context(), proj.Root, d.Argv, s.Runner,
		streamWriter{fw, proto.Stdout}, streamWriter{fw, proto.Stderr})
	entry.Outcome, entry.ExitCode, entry.Duration = "ran", &code, time.Since(start).Round(time.Millisecond).String()
	if r.Context().Err() != nil {
		entry.Outcome = "cancelled"
	}
	if changed := ddevChanged(proj.Root); len(changed) > 0 {
		entry.DdevChanged = changed
		fw.frame(proto.Frame{Stream: proto.Stderr, Data: []byte("ddev-bridge: this command changed the .ddev configuration (" +
			strings.Join(changed, ", ") + "); the bridge is paused for this project until the developer runs `ddev bridge approve` on the host\n")})
	}
	s.audit(entry)
	fw.frame(proto.Frame{Exit: &code})
}

// ddevChanged lists the .ddev/ changes since the last approval, read fresh
// from the registry in case the developer approved while the command ran.
func ddevChanged(root string) []string {
	reg, err := registry.Load()
	if err != nil {
		return nil
	}
	var approved integrity.Fingerprint
	for _, p := range reg.Projects {
		if p.Root == root {
			approved = p.Approved
		}
	}
	if approved == nil {
		return nil
	}
	fp, err := integrity.Snapshot(root)
	if err != nil {
		return nil
	}
	diff := integrity.Diff(approved, fp)
	if len(diff) > 10 {
		diff = append(diff[:10], "…")
	}
	return diff
}
