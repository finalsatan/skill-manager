package schedule

import (
	"strings"
	"testing"
	"time"
)

func TestCronSchedule(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Minute: "*/30 * * * *",
		6 * time.Hour:    "0 */6 * * *",
		90 * time.Minute: "0 0 * * *", // not expressible: falls back to daily
	}
	for d, want := range cases {
		if got := cronSchedule(d); got != want {
			t.Errorf("cronSchedule(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestCronLineMarkerAndReplace(t *testing.T) {
	line := cronLine("/opt/my skillet/skillet", time.Hour, 1000)
	if !strings.HasSuffix(line, cronMarker) || !strings.Contains(line, "'/opt/my skillet/skillet'") {
		t.Fatalf("bad line: %s", line)
	}
	tab := "0 1 * * * other\n" + line + "\n"
	got := withoutMarker(tab)
	if len(got) != 1 || got[0] != "0 1 * * * other" {
		t.Fatalf("withoutMarker = %q", got)
	}
}

func TestSystemdUnits(t *testing.T) {
	t.Setenv("SKILLET_CONFIG_DIR", "/tmp/c d")
	svc := systemdService("/usr/bin/skillet")
	for _, want := range []string{`ExecStart="/usr/bin/skillet" check --notify`, `Environment="SKILLET_CONFIG_DIR=/tmp/c d"`} {
		if !strings.Contains(svc, want) {
			t.Errorf("service missing %q:\n%s", want, svc)
		}
	}
	if !strings.Contains(systemdTimer(2*time.Hour), "OnUnitActiveSec=7200s") {
		t.Error("timer interval wrong")
	}
}

func TestPlistAndSchtasks(t *testing.T) {
	p := launchdPlist("/Applications/a&b/skillet", 6*time.Hour)
	if !strings.Contains(p, "a&amp;b") || !strings.Contains(p, "<integer>21600</integer>") {
		t.Errorf("plist:\n%s", p)
	}
	args := schtasksCreateArgs(`C:\Program Files\skillet.exe`, 90*time.Minute)
	if got := strings.Join(args, " "); !strings.Contains(got, `/MO 90`) || !strings.Contains(got, `"C:\Program Files\skillet.exe" check --notify`) {
		t.Errorf("schtasks args: %s", got)
	}
}
