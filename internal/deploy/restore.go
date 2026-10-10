package deploy

import (
	"fmt"

	"skillet/internal/agents"
	"skillet/internal/gitx"
	"skillet/internal/manifest"
)

type RestoreOpts struct {
	Manifest *manifest.Manifest
	Scope    agents.Scope
	DryRun   bool
	Prune    bool // also disable skills that are deployed but not in the manifest
}

// RepoStep is what restore did (or would do) about one manifest repo.
type RepoStep struct {
	ID, URL string
	Status  string // "present", "cloned", "would clone", "failed"
	Warning string
	Err     error
}

// RestoreResult is everything a restore did, for the CLI to print.
type RestoreResult struct {
	Repos   []RepoStep
	Results []Result // enable results in manifest order, then prune disables
	// Skipped explains skills that were not touched without being an error
	// (their repo is not cloned yet in a dry run).
	Skipped []string
	// Kept lists deployed skills missing from the manifest that were left alone.
	Kept []string
	// Problems are failures; the others were still restored.
	Problems []string
}

// Restore makes the project's skills match the manifest: it clones missing
// repos, enables each skill for exactly the listed agents at the listed pin,
// and (with Prune) removes skills the manifest does not list. A failing
// repo or skill is recorded in Problems and the rest carries on.
func (m *Manager) Restore(o RestoreOpts) (*RestoreResult, error) {
	if o.Scope.Global {
		return nil, errNoProjectManifest
	}
	// Reject the whole manifest before touching anything.
	for _, s := range o.Manifest.Skills {
		if _, err := m.Agents.Resolve(s.Agents); err != nil {
			return nil, fmt.Errorf("skill %s: %w", s.ID, err)
		}
	}

	res := &RestoreResult{}
	unavailable := map[string]string{} // repo id -> why its skills are skipped
	for _, r := range o.Manifest.Repos {
		step := m.restoreRepo(r, o.DryRun)
		res.Repos = append(res.Repos, step)
		switch step.Status {
		case "failed":
			unavailable[r.ID] = "failed"
			res.Problems = append(res.Problems, fmt.Sprintf("repo %s: %v", r.ID, step.Err))
		case "would clone":
			unavailable[r.ID] = "would clone"
		}
	}

	inManifest := map[string]bool{}
	for _, s := range o.Manifest.Skills {
		inManifest[s.ID] = true
		switch unavailable[manifest.RepoOf(s.ID)] {
		case "failed":
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: skipped, its repo could not be cloned", s.ID))
			continue
		case "would clone":
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: skipped, its repo is cloned first on a real run", s.ID))
			continue
		}
		r, note, err := m.restoreSkill(s, o)
		switch {
		case err != nil:
			res.Problems = append(res.Problems, fmt.Sprintf("%s: %v", s.ID, err))
		case note != "":
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %s", s.ID, note))
		default:
			res.Results = append(res.Results, r)
		}
	}

	extra := map[string]bool{}
	var extras []string
	for _, d := range m.St.Deployments {
		if inScope(d, o.Scope) && !inManifest[d.Skill] && !extra[d.Skill] {
			extra[d.Skill] = true
			extras = append(extras, d.Skill)
		}
	}
	for _, id := range extras {
		if !o.Prune {
			res.Kept = append(res.Kept, id)
			continue
		}
		rs, err := m.Disable(ChangeOpts{Skills: []string{id}, Scope: o.Scope, DryRun: o.DryRun})
		res.Results = append(res.Results, rs...)
		if err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("%s: %v", id, err))
		}
	}
	return res, nil
}

// restoreRepo makes sure a manifest repo is in the store.
func (m *Manager) restoreRepo(r manifest.Repo, dryRun bool) RepoStep {
	step := RepoStep{ID: r.ID, URL: r.URL}
	if have, ok := m.St.Repos[r.ID]; ok {
		step.Status = "present"
		if r.Branch != "" && have.Branch != r.Branch {
			step.Warning = fmt.Sprintf("store tracks branch %s, skillet.toml says %s; keeping %s", have.Branch, r.Branch, have.Branch)
		}
		return step
	}
	if dryRun {
		step.Status = "would clone"
		return step
	}
	id, _, err := m.AddRepo(r.URL, r.Branch)
	if err == nil && id != r.ID {
		// Undo what was just cloned: it is not the repo the manifest names.
		_, rerr := m.RemoveRepo(id, false)
		err = fmt.Errorf("%s clones as %s, not the listed id", r.URL, id)
		if rerr != nil {
			err = fmt.Errorf("%w (and removing it failed: %v)", err, rerr)
		}
	}
	if err != nil {
		step.Status, step.Err = "failed", err
		return step
	}
	step.Status = "cloned"
	return step
}

// restoreSkill enables one skill for exactly the manifest's agents and pin.
// A non-empty note means the skill was skipped in a dry run.
func (m *Manager) restoreSkill(s manifest.Skill, o RestoreOpts) (Result, string, error) {
	opts := ChangeOpts{Skills: []string{s.ID}, Agents: s.Agents, Scope: o.Scope, Pin: s.Pin, Unpin: s.Pin == "", DryRun: o.DryRun}
	// want ignores the current agents, so extra ones are dropped.
	exact := func([]string) []string { return s.Agents }
	if _, ok := m.St.Skills[s.ID]; !ok {
		return Result{}, "", fmt.Errorf("not found in %s (try `skillet update` if it was added upstream recently)", manifest.RepoOf(s.ID))
	}
	rs, err := m.change(opts, exact, true)
	if err != nil && s.Pin != "" {
		repo := m.St.Repos[manifest.RepoOf(s.ID)]
		dir := m.RepoDir(repo)
		if _, rerr := gitx.ResolveCommit(dir, s.Pin); rerr != nil {
			if o.DryRun {
				return Result{}, "pin " + s.Pin + " is not in the local clone; a real run fetches it", nil
			}
			if _, ferr := gitx.Fetch(dir, repo.Branch); ferr != nil {
				return Result{}, "", fmt.Errorf("pin %s not in the clone and fetching failed: %w", s.Pin, ferr)
			}
			rs, err = m.change(opts, exact, true)
		}
	}
	if err != nil {
		return Result{}, "", err
	}
	return rs[0], "", nil
}
