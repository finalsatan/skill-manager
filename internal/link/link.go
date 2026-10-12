// Package link creates, inspects and removes skill links.
//
// Links are directory symlinks. On Windows, when symlinks need privileges the
// user lacks, a directory junction is used instead, and a tracked copy is the
// last resort. Every removal first checks that the target is something skillet
// made, so a user's own directory is never deleted.
package link

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/finalsatan/skill-manager/internal/skill"
)

type Mode string

const (
	Auto     Mode = "auto"
	Symlink  Mode = "symlink"
	Junction Mode = "junction"
	Copy     Mode = "copy"
)

func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(s)); m {
	case "", Auto:
		return Auto, nil
	case Symlink, Junction, Copy:
		return m, nil
	}
	return "", fmt.Errorf("unknown link mode %q (auto, symlink, junction, copy)", s)
}

// MarkerFile sits inside a copied skill and records where it came from.
const MarkerFile = ".skillet-source.json"

type Marker struct {
	Skill  string `json:"skill"`
	Source string `json:"source"`
	Hash   string `json:"hash"`
}

type Kind int

const (
	Missing     Kind = iota
	Link             // symlink or junction
	ManagedCopy      // directory with a skillet marker
	Other            // anything else: never touched
)

type Target struct {
	Kind   Kind
	Dest   string  // link destination, absolute (Kind == Link)
	Marker *Marker // Kind == ManagedCopy
}

// Inspect classifies what currently exists at path.
func Inspect(path string) (Target, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Target{Kind: Missing}, nil
	}
	if err != nil {
		return Target{}, err
	}
	if dest, ok := readLink(path, fi); ok {
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(path), dest)
		}
		return Target{Kind: Link, Dest: filepath.Clean(dest)}, nil
	}
	if fi.IsDir() {
		if m, err := ReadMarker(path); err == nil {
			return Target{Kind: ManagedCopy, Marker: m}, nil
		}
	}
	return Target{Kind: Other}, nil
}

func ReadMarker(dir string) (*Marker, error) {
	data, err := os.ReadFile(filepath.Join(dir, MarkerFile))
	if err != nil {
		return nil, err
	}
	m := &Marker{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, err
	}
	return m, nil
}

// PointsTo reports whether a link target resolves to src.
func (t Target) PointsTo(src string) bool {
	if t.Kind != Link {
		return false
	}
	if samePath(t.Dest, src) {
		return true
	}
	a, err1 := filepath.EvalSymlinks(t.Dest)
	b, err2 := filepath.EvalSymlinks(src)
	return err1 == nil && err2 == nil && samePath(a, b)
}

// Within reports whether path lies inside root (both resolved if possible).
func Within(path, root string) bool {
	if within(path, root) {
		return true
	}
	p, err1 := filepath.EvalSymlinks(path)
	r, err2 := filepath.EvalSymlinks(root)
	return err1 == nil && err2 == nil && within(p, r)
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Create links dst to the skill directory src and returns the mode used.
// dst must not exist. For copies, hash is the content hash of src.
func Create(src, dst string, mode Mode, skillID string) (used Mode, hash string, err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", "", err
	}
	switch mode {
	case Symlink:
		return Symlink, "", os.Symlink(src, dst)
	case Junction:
		return Junction, "", createJunction(src, dst)
	case Copy:
		h, err := copySkill(src, dst, skillID)
		return Copy, h, err
	case Auto, "":
		if err := os.Symlink(src, dst); err == nil {
			return Symlink, "", nil
		} else if junctionSupported {
			if jerr := createJunction(src, dst); jerr == nil {
				return Junction, "", nil
			}
		}
		h, err := copySkill(src, dst, skillID)
		return Copy, h, err
	}
	return "", "", fmt.Errorf("unknown link mode %q", mode)
}

// Remove deletes a link or a skillet-managed copy. Anything else is refused.
func Remove(path string) error {
	t, err := Inspect(path)
	if err != nil {
		return err
	}
	switch t.Kind {
	case Missing:
		return nil
	case Link:
		// os.Remove deletes the link itself (or the junction on Windows),
		// never what it points to. Never use RemoveAll here.
		return os.Remove(path)
	case ManagedCopy:
		return os.RemoveAll(path)
	}
	return fmt.Errorf("%s is not managed by skillet; refusing to remove it", path)
}

func copySkill(src, dst, skillID string) (string, error) {
	hash, err := skill.Hash(src)
	if err != nil {
		return "", err
	}
	if err := copyDir(src, dst); err != nil {
		os.RemoveAll(dst)
		return "", err
	}
	data, _ := json.MarshalIndent(Marker{Skill: skillID, Source: src, Hash: hash}, "", "  ")
	if err := os.WriteFile(filepath.Join(dst, MarkerFile), data, 0o644); err != nil {
		os.RemoveAll(dst)
		return "", err
	}
	return hash, nil
}

// copyDir copies regular files (following file symlinks) and skips .git.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			if p != src && d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(out, 0o755)
		}
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			return nil // dangling or directory symlink: skip
		}
		return copyFile(p, out, fi.Mode().Perm())
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}
