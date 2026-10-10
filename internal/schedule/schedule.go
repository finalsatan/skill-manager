// Package schedule registers a periodic `skillet check --notify` with the
// platform's scheduler: systemd user timer or crontab, launchd, schtasks.
package schedule

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	// Name identifies the job in every scheduler.
	Name = "skillet-check"
	// cronMarker tags our crontab line so it can be found and replaced.
	cronMarker = "# skillet-check"
	label      = "io.skillet.check"
)

// Options configure Install and Remove.
type Options struct {
	Exe    string        // absolute path of the skillet binary
	Every  time.Duration // at least one minute
	DryRun bool          // print what would be done, change nothing
	Out    func(format string, a ...any)
}

func (o *Options) say(format string, a ...any) {
	if o.Out != nil {
		o.Out(format, a...)
	}
}

// Install registers (or replaces) the periodic check.
func Install(o Options) (string, error) {
	if o.Exe == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		o.Exe = exe
	}
	if o.Every < time.Minute {
		return "", fmt.Errorf("interval %s is too short (minimum 1m)", o.Every)
	}
	switch runtime.GOOS {
	case "linux":
		return installLinux(o)
	case "darwin":
		return installDarwin(o)
	case "windows":
		return installWindows(o)
	}
	return "", fmt.Errorf("scheduling is not supported on %s", runtime.GOOS)
}

// Remove unregisters the periodic check; it is not an error if none exists.
func Remove(o Options) (string, error) {
	switch runtime.GOOS {
	case "linux":
		return removeLinux(o)
	case "darwin":
		return removeDarwin(o)
	case "windows":
		return removeWindows(o)
	}
	return "", fmt.Errorf("scheduling is not supported on %s", runtime.GOOS)
}

// env returns variables the job needs to find the same configuration.
func env() map[string]string {
	m := map[string]string{}
	for _, k := range []string{"SKILLET_CONFIG_DIR", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if v := os.Getenv(k); v != "" {
			m[k] = v
		}
	}
	return m
}

func sortedKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func run(o Options, name string, args ...string) error {
	o.say("  $ %s %s", name, strings.Join(args, " "))
	if o.DryRun {
		return nil
	}
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func writeFile(o Options, path, content string) error {
	o.say("  write %s", path)
	if o.DryRun {
		o.say("%s", indent(content))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    | " + l
	}
	return strings.Join(lines, "\n")
}

// ---- Linux ----

func installLinux(o Options) (string, error) {
	if hasSystemdUser() {
		dir, err := systemdDir()
		if err != nil {
			return "", err
		}
		if err := writeFile(o, filepath.Join(dir, Name+".service"), systemdService(o.Exe)); err != nil {
			return "", err
		}
		if err := writeFile(o, filepath.Join(dir, Name+".timer"), systemdTimer(o.Every)); err != nil {
			return "", err
		}
		if err := run(o, "systemctl", "--user", "daemon-reload"); err != nil {
			return "", err
		}
		if err := run(o, "systemctl", "--user", "enable", "--now", Name+".timer"); err != nil {
			return "", err
		}
		return "systemd user timer " + Name + ".timer", nil
	}
	return installCron(o)
}

func hasSystemdUser() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

func systemdDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user"), nil
}

// systemdQuote quotes one word of an ExecStart line.
func systemdQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(s)
	return `"` + s + `"`
}

func systemdService(exe string) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Check skillet repos for upstream skill updates\n\n[Service]\nType=oneshot\n")
	e := env()
	for _, k := range sortedKeys(e) {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(k+"="+e[k]))
	}
	fmt.Fprintf(&b, "ExecStart=%s check --notify\n", systemdQuote(exe))
	return b.String()
}

func systemdTimer(every time.Duration) string {
	return fmt.Sprintf(`[Unit]
Description=Periodic skillet update check

[Timer]
OnBootSec=2min
OnUnitActiveSec=%ds
Persistent=true

[Install]
WantedBy=timers.target
`, int(every.Seconds()))
}

func cronSchedule(every time.Duration) string {
	mins := int(every.Minutes())
	switch {
	case mins < 60:
		return fmt.Sprintf("*/%d * * * *", mins)
	case mins%60 == 0 && mins/60 < 24:
		return fmt.Sprintf("0 */%d * * *", mins/60)
	}
	return "0 0 * * *"
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func cronLine(exe string, every time.Duration, uid int) string {
	var vars []string
	e := env()
	for _, k := range sortedKeys(e) {
		vars = append(vars, k+"="+shQuote(e[k]))
	}
	// cron has no session bus; point notify-send at the user's.
	vars = append(vars, fmt.Sprintf("DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/%d/bus", uid))
	return fmt.Sprintf("%s %s %s check --notify >/dev/null 2>&1 %s", cronSchedule(every), strings.Join(vars, " "), shQuote(exe), cronMarker)
}

// withoutMarker drops our lines from a crontab.
func withoutMarker(tab string) []string {
	var keep []string
	for _, l := range strings.Split(tab, "\n") {
		if l != "" && !strings.Contains(l, cronMarker) {
			keep = append(keep, l)
		}
	}
	return keep
}

func currentCrontab() string {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return "" // no crontab yet
	}
	return string(out)
}

func writeCrontab(o Options, lines []string) error {
	o.say("  $ crontab -  (%d line(s))", len(lines))
	if o.DryRun {
		o.say("%s", indent(strings.Join(lines, "\n")))
		return nil
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func installCron(o Options) (string, error) {
	if _, err := exec.LookPath("crontab"); err != nil {
		return "", fmt.Errorf("neither a systemd user session nor crontab is available")
	}
	lines := append(withoutMarker(currentCrontab()), cronLine(o.Exe, o.Every, os.Getuid()))
	if err := writeCrontab(o, lines); err != nil {
		return "", err
	}
	return "crontab entry", nil
}

func removeLinux(o Options) (string, error) {
	var done []string
	if dir, err := systemdDir(); err == nil && hasSystemdUser() {
		svc, tmr := filepath.Join(dir, Name+".service"), filepath.Join(dir, Name+".timer")
		if _, err := os.Stat(tmr); err == nil {
			_ = run(o, "systemctl", "--user", "disable", "--now", Name+".timer")
			for _, f := range []string{svc, tmr} {
				o.say("  remove %s", f)
				if !o.DryRun {
					os.Remove(f)
				}
			}
			_ = run(o, "systemctl", "--user", "daemon-reload")
			done = append(done, "systemd timer")
		}
	}
	if _, err := exec.LookPath("crontab"); err == nil {
		tab := currentCrontab()
		if strings.Contains(tab, cronMarker) {
			if err := writeCrontab(o, withoutMarker(tab)); err != nil {
				return "", err
			}
			done = append(done, "crontab entry")
		}
	}
	return strings.Join(done, ", "), nil
}

// ---- macOS ----

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func xmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func launchdPlist(exe string, every time.Duration) string {
	var envXML string
	if e := env(); len(e) > 0 {
		envXML = "\t<key>EnvironmentVariables</key>\n\t<dict>\n"
		for _, k := range sortedKeys(e) {
			envXML += fmt.Sprintf("\t\t<key>%s</key>\n\t\t<string>%s</string>\n", xmlEsc(k), xmlEsc(e[k]))
		}
		envXML += "\t</dict>\n"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>check</string>
		<string>--notify</string>
	</array>
%s	<key>StartInterval</key>
	<integer>%d</integer>
	<key>RunAtLoad</key>
	<false/>
</dict>
</plist>
`, label, xmlEsc(exe), envXML, int(every.Seconds()))
}

func installDarwin(o Options) (string, error) {
	p, err := plistPath()
	if err != nil {
		return "", err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if _, err := os.Stat(p); err == nil && !o.DryRun {
		_ = exec.Command("launchctl", "bootout", domain, p).Run()
	}
	if err := writeFile(o, p, launchdPlist(o.Exe, o.Every)); err != nil {
		return "", err
	}
	if err := run(o, "launchctl", "bootstrap", domain, p); err != nil {
		return "", err
	}
	return "launchd agent " + label, nil
}

func removeDarwin(o Options) (string, error) {
	p, err := plistPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err != nil {
		return "", nil
	}
	_ = run(o, "launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), p)
	o.say("  remove %s", p)
	if !o.DryRun {
		os.Remove(p)
	}
	return "launchd agent " + label, nil
}

// ---- Windows ----

func schtasksCreateArgs(exe string, every time.Duration) []string {
	mins := int(every.Minutes())
	// /TR is parsed by schtasks, so the inner quotes around the path matter.
	return []string{"/Create", "/F", "/TN", Name, "/SC", "MINUTE", "/MO", fmt.Sprint(mins),
		"/TR", fmt.Sprintf(`"%s" check --notify`, exe)}
}

func installWindows(o Options) (string, error) {
	if int(o.Every.Minutes()) > 1439 {
		return "", fmt.Errorf("schtasks supports intervals up to 1439 minutes (23h59m)")
	}
	if err := run(o, "schtasks", schtasksCreateArgs(o.Exe, o.Every)...); err != nil {
		return "", err
	}
	return "scheduled task " + Name, nil
}

func removeWindows(o Options) (string, error) {
	if !o.DryRun && exec.Command("schtasks", "/Query", "/TN", Name).Run() != nil {
		return "", nil
	}
	if err := run(o, "schtasks", "/Delete", "/F", "/TN", Name); err != nil {
		return "", err
	}
	return "scheduled task " + Name, nil
}
