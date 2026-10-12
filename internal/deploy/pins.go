package deploy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/finalsatan/skill-manager/internal/gitx"
	"github.com/finalsatan/skill-manager/internal/skill"
	"github.com/finalsatan/skill-manager/internal/state"
)

// pinShort is the length of the sha in a pin worktree's directory name.
const pinShort = 7

// splitSkillID splits a skill id into its repo id and in-repo path.
func splitSkillID(id string) (repo, rel string) {
	repo, rel, _ = strings.Cut(id, ":")
	return repo, rel
}

func (m *Manager) pinsRoot() string { return filepath.Join(m.Cfg.Store, "pins") }

// PinDir is where the worktree of repo at commit sha lives.
func (m *Manager) PinDir(repoID, sha string) string {
	if len(sha) > pinShort {
		sha = sha[:pinShort]
	}
	return filepath.Join(m.pinsRoot(), filepath.FromSlash(repoID)+"@"+sha)
}

// DeploymentSource is the directory a deployment is linked or copied from:
// the skill in the main clone, or in the pin worktree for a pinned one. A
// pinned deployment keeps working after upstream moves or deletes the skill,
// so its path comes from the skill id, not from the current scan.
func (m *Manager) DeploymentSource(d *state.Deployment) (string, error) {
	if d.Pin == "" {
		return m.SkillDir(d.Skill)
	}
	repo, rel := splitSkillID(d.Skill)
	if _, ok := m.St.Repos[repo]; !ok {
		return "", fmt.Errorf("skill %s belongs to missing repo %s", d.Skill, repo)
	}
	return filepath.Join(m.PinDir(repo, d.Pin), filepath.FromSlash(rel)), nil
}

// pinSource is the source dir of skill id once pinned to commit sha.
func (m *Manager) pinSource(id, sha string) string {
	repo, rel := splitSkillID(id)
	return filepath.Join(m.PinDir(repo, sha), filepath.FromSlash(rel))
}

// resolvePin turns a revision into a full sha and checks that the skill
// exists at that commit.
func (m *Manager) resolvePin(id, rev string) (string, error) {
	sk := m.St.Skills[id]
	r, ok := m.St.Repos[sk.Repo]
	if !ok {
		return "", fmt.Errorf("skill %s belongs to missing repo %s", id, sk.Repo)
	}
	dir := m.RepoDir(r)
	sha, err := gitx.ResolveCommit(dir, rev)
	if err != nil {
		return "", err
	}
	if !gitx.Exists(dir, sha, path.Join(sk.RelPath, skill.FileName)) {
		return "", fmt.Errorf("skill %s does not exist at %s (%s)", id, rev, sha[:pinShort])
	}
	return sha, nil
}

// ensurePin creates the worktree of repo at sha unless it is already there.
func (m *Manager) ensurePin(repoID, sha string) error {
	r, ok := m.St.Repos[repoID]
	if !ok {
		return fmt.Errorf("unknown repo %s", repoID)
	}
	p := m.PinDir(repoID, sha)
	if fi, err := os.Stat(p); err == nil {
		if head, herr := gitx.Head(p); fi.IsDir() && herr == nil && head == sha {
			return nil
		}
		return fmt.Errorf("%s exists but is not a checkout of %s", p, sha[:pinShort])
	}
	dir := m.RepoDir(r)
	gitx.WorktreePrune(dir) // forget a registration whose directory was deleted
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return gitx.WorktreeAdd(dir, p, sha)
}

// pinCheckout is a worktree found under <store>/pins.
type pinCheckout struct {
	Path string
	Repo string
}

// pinCheckouts lists the worktrees on disk, found by their .git file.
func (m *Manager) pinCheckouts() []pinCheckout {
	root := m.pinsRoot()
	var out []pinCheckout
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == root {
			return nil
		}
		if _, err := os.Lstat(filepath.Join(p, ".git")); err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		repo := filepath.ToSlash(rel)
		if i := strings.LastIndex(repo, "@"); i >= 0 {
			repo = repo[:i]
		}
		out = append(out, pinCheckout{Path: p, Repo: repo})
		return filepath.SkipDir
	})
	return out
}

// unusedPins lists worktrees that no deployment is pinned to.
func (m *Manager) unusedPins() []pinCheckout {
	used := map[string]bool{}
	for _, d := range m.St.Deployments {
		if d.Pin != "" {
			repo, _ := splitSkillID(d.Skill)
			used[m.PinDir(repo, d.Pin)] = true
		}
	}
	var out []pinCheckout
	for _, c := range m.pinCheckouts() {
		if !used[c.Path] {
			out = append(out, c)
		}
	}
	return out
}

// removePin deletes one pin worktree through git, falling back to removing
// the plain directory when git no longer knows it, then drops empty parents.
func (m *Manager) removePin(c pinCheckout) error {
	var repoDir string
	if r, ok := m.St.Repos[c.Repo]; ok {
		repoDir = m.RepoDir(r)
	}
	var err error
	if repoDir != "" {
		err = gitx.WorktreeRemove(repoDir, c.Path)
	} else {
		err = errors.New("repo is not registered")
	}
	if err != nil {
		// Never RemoveAll through a link; a worktree is a plain directory.
		fi, serr := os.Lstat(c.Path)
		if serr != nil || !fi.IsDir() {
			return err
		}
		if rerr := os.RemoveAll(c.Path); rerr != nil {
			return errors.Join(err, rerr)
		}
	}
	if repoDir != "" {
		gitx.WorktreePrune(repoDir)
	}
	root := m.pinsRoot()
	for p := filepath.Dir(c.Path); p != root && strings.HasPrefix(p, root); p = filepath.Dir(p) {
		if os.Remove(p) != nil {
			break
		}
	}
	return nil
}

// gcPins removes the worktrees no deployment uses and returns their paths.
func (m *Manager) gcPins() ([]string, error) {
	var removed []string
	var errs []error
	for _, c := range m.unusedPins() {
		if err := m.removePin(c); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, c.Path)
	}
	sort.Strings(removed)
	return removed, errors.Join(errs...)
}

// removeRepoPins deletes every pin worktree of a repo.
func (m *Manager) removeRepoPins(repoID string) error {
	var errs []error
	for _, c := range m.pinCheckouts() {
		if c.Repo == repoID {
			errs = append(errs, m.removePin(c))
		}
	}
	return errors.Join(errs...)
}

// pinNote summarizes the pinned deployments of a repo that an update skips.
func (m *Manager) pinNote(repoID string) []string {
	n := 0
	shorts := map[string]bool{}
	for _, d := range m.St.Deployments {
		if repo, _ := splitSkillID(d.Skill); d.Pin != "" && repo == repoID {
			n++
			shorts[d.Pin[:pinShort]] = true
		}
	}
	if n == 0 {
		return nil
	}
	var pins []string
	for s := range shorts {
		pins = append(pins, s)
	}
	sort.Strings(pins)
	return []string{fmt.Sprintf("%d pinned deployment(s) not updated (pinned to %s)", n, strings.Join(pins, ", "))}
}
