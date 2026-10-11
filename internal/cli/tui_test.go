package cli

import (
	"bytes"
	"strings"
	"testing"
)

// uninitialized points the config at an empty directory and pretends to be on
// a terminal, recording whether the TUI was started.
func uninitialized(t *testing.T) *bool {
	t.Helper()
	t.Setenv("SKILLET_CONFIG_DIR", t.TempDir())
	t.Setenv("SKILLET_NO_INPUT", "")
	t.Setenv("CI", "")
	oldTTY, oldStart := stdinStdoutAreTTY, startTUI
	started := false
	stdinStdoutAreTTY = func() bool { return true }
	startTUI = func() error { started = true; return nil }
	t.Cleanup(func() { stdinStdoutAreTTY, startTUI = oldTTY, oldStart })
	return &started
}

func TestBareSkilletNotInitializedShowsHint(t *testing.T) {
	started := uninitialized(t)
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(nil)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if *started {
		t.Error("TUI started without a config")
	}
	if !strings.Contains(out.String(), "Usage:") || !strings.Contains(out.String(), "skillet init") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestTUICommandNotInitializedFails(t *testing.T) {
	started := uninitialized(t)
	root := NewRoot()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"tui"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "skillet init") {
		t.Fatalf("err = %v", err)
	}
	if *started {
		t.Error("TUI started without a config")
	}
}
