package server

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"ddev-bridge/internal/policy"
	"ddev-bridge/internal/runner"
)

const (
	// drushListTimeout bounds `ddev drush list --format=json`.
	drushListTimeout = time.Minute
	// drushRefreshEvery limits re-listing after unknown names (typos, loops).
	drushRefreshEvery = 30 * time.Second
)

// drushList caches one project's drush commands in memory.
type drushList struct {
	mu      sync.Mutex
	cmds    *policy.DrushCommands
	fetched time.Time
}

func (s *Server) drushListFor(root string) *drushList {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drush == nil {
		s.drush = map[string]*drushList{}
	}
	if s.drush[root] == nil {
		s.drush[root] = &drushList{}
	}
	return s.drush[root]
}

// drushLister lists the project's drush commands on first use and again when
// policy asks for a refresh, at most every drushRefreshEvery. Without fetch
// (the early check, while another command may hold the project) it only uses
// the cache and otherwise defers with policy.ErrDrushNotListed.
func (s *Server) drushLister(ctx context.Context, root string, fetch bool) policy.DrushLister {
	return func(refresh bool) (*policy.DrushCommands, error) {
		l := s.drushListFor(root)
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.cmds != nil && (!refresh || time.Since(l.fetched) < drushRefreshEvery) {
			return l.cmds, nil
		}
		if !fetch {
			return nil, policy.ErrDrushNotListed
		}
		cmds, err := s.fetchDrushList(ctx, root)
		if err != nil {
			if l.cmds != nil {
				return l.cmds, nil // keep using the last good list
			}
			return nil, err
		}
		l.cmds, l.fetched = cmds, time.Now()
		return cmds, nil
	}
}

func (s *Server) fetchDrushList(ctx context.Context, root string) (*policy.DrushCommands, error) {
	o := s.Runner
	if o.Timeout == 0 || o.Timeout > drushListTimeout {
		o.Timeout = drushListTimeout
	}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(ctx, root, []string{"drush", "list", "--format=json"}, o, &stdout, &stderr); code != 0 {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return nil, fmt.Errorf("`ddev drush list` exited with %d: %s", code, msg)
	}
	return policy.ParseDrushList(stdout.Bytes())
}
