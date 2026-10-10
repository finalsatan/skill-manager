// Package state persists repos, skills and deployments in <store>/state.json.
//
// All access goes through Open, which holds an exclusive file lock until
// Close, so a scheduled `skillet check` and a manual command never interleave.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
)

const currentVersion = 1

type Repo struct {
	URL         string    `json:"url"`
	Branch      string    `json:"branch"`
	Path        string    `json:"path"` // relative to the store
	Head        string    `json:"head"`
	Upstream    string    `json:"upstream,omitempty"`
	LastChecked time.Time `json:"last_checked,omitempty"`
	Notified    string    `json:"notified,omitempty"` // upstream commit a desktop notification was already sent for
}

type Skill struct {
	Repo        string `json:"repo"`
	RelPath     string `json:"rel_path"` // skill directory inside the repo, slash-separated
	Name        string `json:"name"`
	Description string `json:"description"`
	Pending     bool   `json:"pending,omitempty"`
}

// Deployment is one link (or copy) on disk. Several requested agents may be
// served by the same link, e.g. OpenCode also reads .claude/skills.
type Deployment struct {
	Skill      string    `json:"skill"`
	Agents     []string  `json:"agents"`
	Scope      string    `json:"scope"` // "project" or "global"
	Dir        string    `json:"dir,omitempty"`
	Target     string    `json:"target"`
	Mode       string    `json:"mode"`
	Pin        string    `json:"pin,omitempty"`
	Alias      string    `json:"alias,omitempty"` // link name chosen with `enable --as`; empty means the skill's own name
	SourceHash string    `json:"source_hash,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

const (
	ScopeProject = "project"
	ScopeGlobal  = "global"
)

type State struct {
	Version     int               `json:"version"`
	Repos       map[string]*Repo  `json:"repos"`
	Skills      map[string]*Skill `json:"skills"`
	Deployments []*Deployment     `json:"deployments"`
}

// Handle is an open, locked state file.
type Handle struct {
	*State
	path string
	lock *flock.Flock
}

// ErrLocked is returned by TryOpen when another skillet process holds the state.
var ErrLocked = errors.New("state is locked by another skillet process")

func Open(store string) (*Handle, error) { return open(store, true) }

// TryOpen is Open without waiting: it returns ErrLocked if the state is busy.
// Background checks use it so they never queue behind an interactive command.
func TryOpen(store string) (*Handle, error) { return open(store, false) }

func open(store string, wait bool) (*Handle, error) {
	if err := os.MkdirAll(store, 0o755); err != nil {
		return nil, err
	}
	lk := flock.New(filepath.Join(store, "state.lock"))
	if wait {
		if err := lk.Lock(); err != nil {
			return nil, fmt.Errorf("lock state: %w", err)
		}
	} else {
		ok, err := lk.TryLock()
		if err != nil {
			return nil, fmt.Errorf("lock state: %w", err)
		}
		if !ok {
			return nil, ErrLocked
		}
	}
	h := &Handle{path: filepath.Join(store, "state.json"), lock: lk}
	st, err := load(h.path)
	if err != nil {
		lk.Unlock()
		return nil, err
	}
	h.State = st
	return h, nil
}

// Peek reads the state without locking. Writes are atomic renames, so a
// concurrent writer yields either the old or the new file, never a torn one.
func Peek(store string) (*State, error) {
	return load(filepath.Join(store, "state.json"))
}

func load(path string) (*State, error) {
	st := &State{Version: currentVersion}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, st); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if st.Version > currentVersion {
			return nil, fmt.Errorf("%s was written by a newer skillet (version %d)", path, st.Version)
		}
	}
	if st.Repos == nil {
		st.Repos = map[string]*Repo{}
	}
	if st.Skills == nil {
		st.Skills = map[string]*Skill{}
	}
	return st, nil
}

// Save writes the state atomically.
func (h *Handle) Save() error {
	sort.SliceStable(h.Deployments, func(i, j int) bool {
		return h.Deployments[i].Target < h.Deployments[j].Target
	})
	data, err := json.MarshalIndent(h.State, "", "  ")
	if err != nil {
		return err
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, h.path)
}

func (h *Handle) Close() error {
	return h.lock.Unlock()
}

// SkillID builds the stable identifier of a skill: <repo>:<rel path>.
func SkillID(repo, relPath string) string {
	return repo + ":" + relPath
}

// DeploymentAt returns the deployment whose link lives at target.
func (s *State) DeploymentAt(target string) *Deployment {
	for _, d := range s.Deployments {
		if d.Target == target {
			return d
		}
	}
	return nil
}

func (s *State) RemoveDeployment(target string) {
	out := s.Deployments[:0]
	for _, d := range s.Deployments {
		if d.Target != target {
			out = append(out, d)
		}
	}
	s.Deployments = out
}

// DeploymentsOf lists all deployments of one skill.
func (s *State) DeploymentsOf(skillID string) []*Deployment {
	var out []*Deployment
	for _, d := range s.Deployments {
		if d.Skill == skillID {
			out = append(out, d)
		}
	}
	return out
}
