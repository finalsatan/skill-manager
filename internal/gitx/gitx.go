// Package gitx runs the system git binary, so users keep their own SSH keys
// and credential helpers.
package gitx

import (
	"bytes"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Run executes git in dir and returns trimmed stdout.
func Run(dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	// Keep leading whitespace: diffstat lines are indented.
	return strings.TrimRight(stdout.String(), " \t\r\n"), nil
}

func Clone(url, dst, branch string) error {
	args := []string{"clone", "--quiet"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	_, err := Run("", append(args, "--", url, dst)...)
	return err
}

func Head(dir string) (string, error) {
	return Run(dir, "rev-parse", "HEAD")
}

func CurrentBranch(dir string) (string, error) {
	return Run(dir, "symbolic-ref", "--short", "HEAD")
}

// Source is a parsed repository reference.
type Source struct {
	URL string // what git clones
	ID  string // host/owner/repo, used as the store path and repo key
}

var (
	shorthand = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	scpLike   = regexp.MustCompile(`^[A-Za-z0-9_.-]+@([A-Za-z0-9_.-]+):(.+)$`)
)

// ParseSource accepts owner/repo (GitHub), https/ssh/git URLs, scp-like
// git@host:owner/repo, and local paths or file:// URLs.
func ParseSource(ref string) (Source, error) {
	src, err := parseSource(ref)
	if err != nil {
		return src, err
	}
	// The ID becomes a path under the store, so it must not escape it.
	for _, seg := range strings.Split(src.ID, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return Source{}, fmt.Errorf("invalid repository reference %q: bad path segment %q", ref, seg)
		}
	}
	return src, nil
}

func parseSource(ref string) (Source, error) {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return Source{}, fmt.Errorf("empty repository reference")
	case shorthand.MatchString(ref) && !isLocalPath(ref):
		return Source{URL: "https://github.com/" + strings.TrimSuffix(ref, ".git") + ".git",
			ID: "github.com/" + strings.TrimSuffix(ref, ".git")}, nil
	case scpLike.MatchString(ref):
		m := scpLike.FindStringSubmatch(ref)
		return Source{URL: ref, ID: strings.ToLower(m[1]) + "/" + cleanRepoPath(m[2])}, nil
	case strings.HasPrefix(ref, "file://"):
		u, err := url.Parse(ref)
		if err != nil {
			return Source{}, err
		}
		return localSource(ref, u.Path), nil
	case strings.Contains(ref, "://"):
		u, err := url.Parse(ref)
		if err != nil {
			return Source{}, err
		}
		p := cleanRepoPath(u.Path)
		if u.Host == "" || p == "" {
			return Source{}, fmt.Errorf("cannot parse repository URL %q", ref)
		}
		cloneURL := ref
		// Browser URLs like https://github.com/o/r/tree/main/x clone as o/r.
		if parts := strings.Split(p, "/"); len(parts) > 2 && (parts[2] == "tree" || parts[2] == "blob") {
			p = parts[0] + "/" + parts[1]
			u.Path = "/" + p + ".git"
			cloneURL = u.String()
		}
		return Source{URL: cloneURL, ID: strings.ToLower(u.Hostname()) + "/" + p}, nil
	default:
		abs, err := filepath.Abs(ref)
		if err != nil {
			return Source{}, err
		}
		return localSource(abs, abs), nil
	}
}

func localSource(cloneURL, p string) Source {
	name := strings.TrimSuffix(path.Base(filepath.ToSlash(p)), ".git")
	return Source{URL: cloneURL, ID: "local/" + name}
}

func isLocalPath(ref string) bool {
	return strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "/")
}

func cleanRepoPath(p string) string {
	p = strings.Trim(p, "/")
	return strings.TrimSuffix(p, ".git")
}

// Fetch updates origin/<branch> and returns its commit.
func Fetch(dir, branch string) (string, error) {
	if _, err := Run(dir, "fetch", "--quiet", "origin", branch); err != nil {
		return "", err
	}
	return Run(dir, "rev-parse", "refs/remotes/origin/"+branch)
}

// ChangedFiles lists files that differ between two commits, slash-separated.
func ChangedFiles(dir, from, to string) ([]string, error) {
	out, err := Run(dir, "diff", "--name-only", "--no-renames", from, to)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// Exists reports whether path exists in the tree of commit rev.
func Exists(dir, rev, path string) bool {
	_, err := Run(dir, "cat-file", "-e", rev+":"+path)
	return err == nil
}

// CountCommits counts commits reachable from to but not from.
func CountCommits(dir, from, to string) (int, error) {
	out, err := Run(dir, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}
	var n int
	_, err = fmt.Sscanf(out, "%d", &n)
	return n, err
}

// Log returns one-line commit summaries in from..to touching paths.
func Log(dir, from, to string, paths ...string) (string, error) {
	args := []string{"log", "--oneline", "--no-decorate", from + ".." + to}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	return Run(dir, args...)
}

// Diff returns the diff between two commits, limited to paths. With stat
// only the summary is returned.
func Diff(dir, from, to string, stat bool, paths ...string) (string, error) {
	args := []string{"diff", "--no-color"}
	if stat {
		args = append(args, "--stat")
	}
	args = append(args, from, to)
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	return Run(dir, args...)
}

// IsClean reports whether the work tree has no local modifications.
func IsClean(dir string) (bool, error) {
	out, err := Run(dir, "status", "--porcelain", "--untracked-files=no")
	return out == "", err
}

// IsAncestor reports whether a is an ancestor of b.
func IsAncestor(dir, a, b string) bool {
	_, err := Run(dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// FastForward moves the checked-out branch to rev; it fails if that is not
// a fast-forward.
func FastForward(dir, rev string) error {
	_, err := Run(dir, "merge", "--ff-only", "--quiet", rev)
	return err
}
