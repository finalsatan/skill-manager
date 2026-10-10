package link

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func skillDir(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "pdf")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func modes() []Mode {
	m := []Mode{Auto, Copy}
	if runtime.GOOS == "windows" {
		m = append(m, Junction)
	} else {
		m = append(m, Symlink)
	}
	return m
}

func TestCreateInspectRemove(t *testing.T) {
	for _, mode := range modes() {
		t.Run(string(mode), func(t *testing.T) {
			src := skillDir(t)
			dst := filepath.Join(t.TempDir(), "a", "b", "pdf")
			used, hash, err := Create(src, dst, mode, "repo:pdf")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
				t.Fatalf("skill not reachable through %s: %v", used, err)
			}
			tg, err := Inspect(dst)
			if err != nil {
				t.Fatal(err)
			}
			switch used {
			case Copy:
				if tg.Kind != ManagedCopy || tg.Marker.Hash != hash || hash == "" {
					t.Fatalf("copy inspected as %+v", tg)
				}
			default:
				if tg.Kind != Link || !tg.PointsTo(src) {
					t.Fatalf("link inspected as %+v", tg)
				}
			}
			if err := Remove(dst); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(dst); !os.IsNotExist(err) {
				t.Fatal("target still exists")
			}
			if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
				t.Fatal("removing the link damaged the source")
			}
		})
	}
}

func TestRemoveRefusesUnmanaged(t *testing.T) {
	dir := skillDir(t)
	if err := Remove(dir); err == nil {
		t.Fatal("Remove deleted a directory skillet does not manage")
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal("unmanaged directory was damaged")
	}
}

func TestWithin(t *testing.T) {
	root := t.TempDir()
	if !Within(filepath.Join(root, "repos", "x"), root) {
		t.Error("child not within root")
	}
	if Within(filepath.Join(root+"-other", "x"), root) {
		t.Error("sibling with common prefix reported within root")
	}
}
