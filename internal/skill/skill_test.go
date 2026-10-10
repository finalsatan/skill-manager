package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "skills/pdf/SKILL.md"), "---\nname: pdf\ndescription: >\n  PDF tools\n---\nbody\n")
	write(t, filepath.Join(root, "skills/pdf/nested/SKILL.md"), "---\nname: inner\ndescription: x\n---\n")
	write(t, filepath.Join(root, "skills/Bad_Name/SKILL.md"), "---\nname: Bad_Name\ndescription: x\n---\n")
	write(t, filepath.Join(root, "node_modules/x/SKILL.md"), "---\nname: x\ndescription: x\n---\n")
	write(t, filepath.Join(root, "other/SKILL.md"), "no frontmatter")

	found, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 {
		t.Fatalf("found %d skills: %+v", len(found), found)
	}
	byPath := map[string]Found{}
	for _, f := range found {
		byPath[f.RelPath] = f
	}
	if f := byPath["skills/pdf"]; f.Name != "pdf" || f.Description != "PDF tools" || len(f.Warnings) != 0 {
		t.Errorf("pdf = %+v", f)
	}
	if f := byPath["skills/Bad_Name"]; len(f.Warnings) == 0 {
		t.Errorf("expected a name warning for %+v", f)
	}
	if f := byPath["other"]; f.Name != "other" || len(f.Warnings) == 0 {
		t.Errorf("other = %+v", f)
	}
}

func TestHashIgnoresSkipped(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "SKILL.md"), "a")
	h1, _ := Hash(dir)
	write(t, filepath.Join(dir, "marker.json"), "{}")
	h2, _ := Hash(dir, "marker.json")
	if h1 != h2 {
		t.Error("skipped file changed the hash")
	}
	write(t, filepath.Join(dir, "SKILL.md"), "b")
	if h3, _ := Hash(dir, "marker.json"); h3 == h1 {
		t.Error("content change did not change the hash")
	}
}
