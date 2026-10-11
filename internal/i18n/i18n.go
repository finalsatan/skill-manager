// Package i18n holds the TUI's message catalog in Chinese and English.
//
// Only the TUI is translated; command-line output stays English. Keys are
// stable identifiers; a test checks that both tables define exactly the same
// keys.
package i18n

import (
	"fmt"
	"strings"
	"sync"
)

// Lang is a supported language.
type Lang string

const (
	EN Lang = "en"
	ZH Lang = "zh"
)

var (
	mu  sync.RWMutex
	cur = EN
)

// Set selects the language used by T.
func Set(l Lang) {
	mu.Lock()
	cur = l
	mu.Unlock()
}

// Current returns the selected language.
func Current() Lang {
	mu.RLock()
	defer mu.RUnlock()
	return cur
}

// T returns the message for key in the current language, formatted with args
// (fmt style). A key missing from the table is returned as is, so a typo is
// visible instead of blank.
func T(key string, args ...any) string { return In(Current(), key, args...) }

// In is T for an explicit language.
func In(l Lang, key string, args ...any) string {
	tab := en
	if l == ZH {
		tab = zh
	}
	msg, ok := tab[key]
	if !ok {
		return key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// Detect picks the language: SKILLET_LANG, then the config file's lang, then
// LC_ALL, LC_MESSAGES and LANG (a zh prefix means Chinese), else English.
// getenv is os.Getenv in production.
func Detect(cfgLang string, getenv func(string) string) Lang {
	if l, ok := parse(getenv("SKILLET_LANG")); ok {
		return l
	}
	if l, ok := parse(cfgLang); ok {
		return l
	}
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := getenv(k); v != "" {
			// The first locale variable that is set decides, as in POSIX.
			if l, ok := parse(v); ok {
				return l
			}
			return EN
		}
	}
	return EN
}

// parse accepts "zh", "en" and locale names such as zh_CN.UTF-8. Anything
// else, including "C" and "POSIX", is not a decision.
func parse(v string) (Lang, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasPrefix(v, "zh"):
		return ZH, true
	case strings.HasPrefix(v, "en"):
		return EN, true
	}
	return "", false
}
