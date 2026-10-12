package deploy

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/finalsatan/skill-manager/internal/agents"
	"github.com/finalsatan/skill-manager/internal/manifest"
	"github.com/finalsatan/skill-manager/internal/state"
)

var errNoProjectManifest = errors.New("skillet.toml belongs to a project; global scope is not supported")

// ManifestPath is where a project's manifest lives.
func ManifestPath(s agents.Scope) string { return filepath.Join(s.Dir, manifest.FileName) }

// BuildManifest derives the manifest of a project from its deployments.
func (m *Manager) BuildManifest(s agents.Scope) (*manifest.Manifest, error) {
	if s.Global {
		return nil, errNoProjectManifest
	}
	var deps []*state.Deployment
	for _, d := range m.St.Deployments {
		if inScope(d, s) {
			deps = append(deps, d)
		}
	}
	return manifest.Build(m.St.Repos, deps, m.Agents.IDs())
}

// SaveManifest writes the project's manifest and returns what it wrote.
func (m *Manager) SaveManifest(s agents.Scope) (*manifest.Manifest, error) {
	mf, err := m.BuildManifest(s)
	if err != nil {
		return nil, err
	}
	return mf, manifest.Save(ManifestPath(s), mf)
}

// SyncManifest rewrites the project's manifest if it already has one; a
// project without skillet.toml is left alone. It reports whether it wrote.
func (m *Manager) SyncManifest(s agents.Scope) (bool, error) {
	if s.Global {
		return false, nil
	}
	if _, err := os.Stat(ManifestPath(s)); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	_, err := m.SaveManifest(s)
	return err == nil, err
}
