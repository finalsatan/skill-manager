package notify

import (
	"strings"
	"testing"
)

func TestAppleScriptQuoting(t *testing.T) {
	_, args, err := command("darwin", `a "b"`, `c\d`)
	if err != nil {
		t.Fatal(err)
	}
	want := `display notification "c\\d" with title "a \"b\""`
	if args[1] != want {
		t.Errorf("got %s\nwant %s", args[1], want)
	}
}

func TestPowerShellEscaping(t *testing.T) {
	s := psToast("it's", "<x> & y")
	if !strings.Contains(s, "it''s") || !strings.Contains(s, "&lt;x&gt; &amp; y") {
		t.Errorf("not escaped:\n%s", s)
	}
}
