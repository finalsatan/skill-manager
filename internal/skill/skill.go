// Package skill finds and parses Agent Skills (directories with SKILL.md).
package skill

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const FileName = "SKILL.md"

// Found is a skill discovered inside a repository.
type Found struct {
	RelPath     string // slash-separated, relative to the scanned root
	Name        string
	Description string
	Warnings    []string
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "__pycache__": true}

// Scan walks root and returns every directory containing SKILL.md. It does
// not descend into a skill once found.
func Scan(root string) ([]Found, error) {
	var out []Found
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(p, FileName)); err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		f := Found{RelPath: filepath.ToSlash(rel)}
		meta, err := ReadMeta(p)
		if err != nil {
			f.Warnings = append(f.Warnings, err.Error())
		}
		f.Name, f.Description = meta.Name, meta.Description
		if f.Name == "" {
			f.Name = d.Name()
			if p == root {
				f.Name = filepath.Base(root)
			}
			f.Warnings = append(f.Warnings, "frontmatter has no name; using directory name")
		}
		if f.Description == "" {
			f.Warnings = append(f.Warnings, "frontmatter has no description; agents may not load it")
		}
		if !ValidName(f.Name) {
			f.Warnings = append(f.Warnings, fmt.Sprintf("name %q is not lowercase-hyphen (1-64 chars); OpenCode will reject it", f.Name))
		}
		if p != root && f.Name != d.Name() {
			f.Warnings = append(f.Warnings, fmt.Sprintf("name %q differs from directory %q", f.Name, d.Name()))
		}
		out = append(out, f)
		return filepath.SkipDir
	})
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, err
}

type Meta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ReadMeta parses the YAML frontmatter of dir/SKILL.md.
func ReadMeta(dir string) (Meta, error) {
	var m Meta
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return m, err
	}
	front, ok := frontmatter(data)
	if !ok {
		return m, fmt.Errorf("SKILL.md has no frontmatter")
	}
	if err := yaml.Unmarshal(front, &m); err != nil {
		return m, fmt.Errorf("SKILL.md frontmatter: %w", err)
	}
	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	return m, nil
}

func frontmatter(data []byte) ([]byte, bool) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return nil, false
	}
	var buf bytes.Buffer
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "---" {
			return buf.Bytes(), true
		}
		buf.WriteString(sc.Text())
		buf.WriteByte('\n')
	}
	return nil, false
}

var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidName applies the strictest rule among agents (OpenCode's).
func ValidName(name string) bool {
	return len(name) >= 1 && len(name) <= 64 && nameRe.MatchString(name)
}

// Hash is a content hash of every regular file under dir, used to detect
// whether a copied skill was edited by hand. Files named in skip are ignored.
func Hash(dir string, skip ...string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		for _, s := range skip {
			if rel == s {
				return nil
			}
		}
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintf(h, "%s\x00", rel)
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
