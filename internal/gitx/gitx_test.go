package gitx

import "testing"

func TestParseSource(t *testing.T) {
	cases := []struct{ in, url, id string }{
		{"anthropics/skills", "https://github.com/anthropics/skills.git", "github.com/anthropics/skills"},
		{"https://github.com/Owner/Repo.git", "https://github.com/Owner/Repo.git", "github.com/Owner/Repo"},
		{"https://github.com/o/r/tree/main/skills/x", "https://github.com/o/r.git", "github.com/o/r"},
		{"git@gitlab.com:group/sub/repo.git", "git@gitlab.com:group/sub/repo.git", "gitlab.com/group/sub/repo"},
		{"ssh://git@example.com:2222/team/repo.git", "ssh://git@example.com:2222/team/repo.git", "example.com/team/repo"},
		{"file:///srv/git/tools.git", "file:///srv/git/tools.git", "local/tools"},
	}
	for _, c := range cases {
		got, err := ParseSource(c.in)
		if err != nil {
			t.Errorf("ParseSource(%q): %v", c.in, err)
			continue
		}
		if got.URL != c.url || got.ID != c.id {
			t.Errorf("ParseSource(%q) = %+v, want url=%s id=%s", c.in, got, c.url, c.id)
		}
	}
}
