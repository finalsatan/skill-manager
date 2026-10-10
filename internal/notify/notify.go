// Package notify shows a desktop notification using the platform's own tools.
package notify

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Send shows a notification. It returns an error when no mechanism is
// available (e.g. a headless Linux box without notify-send).
func Send(title, body string) error {
	name, args, err := command(runtime.GOOS, title, body)
	if err != nil {
		return err
	}
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func command(goos, title, body string) (string, []string, error) {
	switch goos {
	case "linux":
		if _, err := exec.LookPath("notify-send"); err != nil {
			return "", nil, fmt.Errorf("notify-send not found")
		}
		return "notify-send", []string{"--app-name=skillet", title, body}, nil
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s", asQuote(body), asQuote(title))
		return "osascript", []string{"-e", script}, nil
	case "windows":
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", psToast(title, body)}, nil
	}
	return "", nil, fmt.Errorf("notifications are not supported on %s", goos)
}

// asQuote quotes s as an AppleScript string literal.
func asQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
	return `"` + s + `"`
}

// psToast builds a PowerShell script that shows a toast through the WinRT API.
func psToast(title, body string) string {
	esc := func(s string) string {
		s = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "''").Replace(s)
		return s
	}
	return fmt.Sprintf(`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$t = $x.GetElementsByTagName('text')
$t.Item(0).AppendChild($x.CreateTextNode('%s')) | Out-Null
$t.Item(1).AppendChild($x.CreateTextNode('%s')) | Out-Null
$n = [Windows.UI.Notifications.ToastNotification]::new($x)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('skillet').Show($n)`, esc(title), esc(body))
}
