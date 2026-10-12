package tui

import (
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"skillet/internal/i18n"
)

// syncBuffer is an io.Writer the test can read while the program writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return ansi.Strip(b.buf.String())
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestProgramEnablesSkillEndToEnd runs the model in a real tea.Program (no
// terminal: keys come from a pipe, frames go to a buffer) against a real temp
// store: space toggles docx for claude, enter previews, enter applies, q
// quits. It checks the commands are wired through the actual event loop.
func TestProgramEnablesSkillEndToEnd(t *testing.T) {
	rs := newRealStore(t)
	setLang(t, i18n.EN)

	pr, pw := io.Pipe()
	out := &syncBuffer{}
	p := tea.NewProgram(New(Options{Project: rs.proj}),
		tea.WithInput(pr), tea.WithOutput(out), tea.WithWindowSize(100, 30),
		tea.WithoutSignals(), tea.WithEnvironment([]string{"TERM=xterm-256color"}))
	type result struct {
		m   tea.Model
		err error
	}
	done := make(chan result, 1)
	go func() {
		m, err := p.Run()
		done <- result{m, err}
	}()
	send := func(s string) {
		t.Helper()
		if _, err := pw.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}

	// Grouped by default: the one repo starts collapsed (nothing is enabled).
	waitFor(t, "the matrix to load", func() bool { return strings.Contains(out.text(), "2 skills") })
	send("\r") // enter on the group header unfolds it
	waitFor(t, "the group to unfold", func() bool { return strings.Contains(out.text(), "docx") })
	send("j")
	send(" ") // docx, first column (claude): pending enable
	waitFor(t, "the pending summary", func() bool { return strings.Contains(out.text(), "1 changes pending") })
	send("\r")
	waitFor(t, "the preview", func() bool { return strings.Contains(out.text(), "[ Apply ]") })
	if exists(filepath.Join(rs.links, "docx")) {
		t.Fatal("the preview created the link")
	}
	send("\r")
	waitFor(t, "the link on disk", func() bool { return exists(filepath.Join(rs.links, "docx")) })
	// q is ignored while the apply runs, so keep asking until the program ends.
	var r result
	deadline := time.After(20 * time.Second)
wait:
	for {
		send("q")
		select {
		case r = <-done:
			break wait
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			p.Kill()
			t.Fatal("program did not exit")
		}
	}
	if r.err != nil {
		t.Fatalf("program: %v", r.err)
	}
	m := r.m.(Model)
	if m.applying || m.modal.open || m.status != "Enabled docx" || m.statusErr {
		t.Errorf("final state: applying=%v modal=%v status=%q err=%v", m.applying, m.modal.open, m.status, m.statusErr)
	}
	pw.Close()
}
