package i18n

import (
	"fmt"
	"regexp"
	"testing"
)

func TestCatalogsHaveSameKeys(t *testing.T) {
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("key %q missing from zh", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("key %q missing from en", k)
		}
	}
}

var verb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)

func TestCatalogsUseSameVerbs(t *testing.T) {
	for k, e := range en {
		if z, ok := zh[k]; ok {
			if fmt.Sprint(verb.FindAllString(e, -1)) != fmt.Sprint(verb.FindAllString(z, -1)) {
				t.Errorf("key %q: placeholders differ: %q vs %q", k, e, z)
			}
		}
	}
}

func TestDetectOrder(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	cases := []struct {
		name string
		cfg  string
		env  map[string]string
		want Lang
	}{
		{"default", "", nil, EN},
		{"SKILLET_LANG beats config", "en", map[string]string{"SKILLET_LANG": "zh"}, ZH},
		{"SKILLET_LANG beats locale", "", map[string]string{"SKILLET_LANG": "en", "LANG": "zh_CN.UTF-8"}, EN},
		{"config beats locale", "zh", map[string]string{"LANG": "en_US.UTF-8"}, ZH},
		{"config en beats locale zh", "en", map[string]string{"LC_ALL": "zh_CN.UTF-8"}, EN},
		{"LC_ALL beats LANG", "", map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "zh_CN.UTF-8"}, EN},
		{"LC_MESSAGES beats LANG", "", map[string]string{"LC_MESSAGES": "zh_TW.UTF-8", "LANG": "en_US.UTF-8"}, ZH},
		{"LANG alone", "", map[string]string{"LANG": "zh_CN.UTF-8"}, ZH},
		{"C locale is English", "", map[string]string{"LANG": "C"}, EN},
		{"unknown SKILLET_LANG falls through", "", map[string]string{"SKILLET_LANG": "fr", "LANG": "zh_CN"}, ZH},
		{"unknown config falls through", "fr", map[string]string{"LANG": "zh_CN"}, ZH},
	}
	for _, c := range cases {
		if got := Detect(c.cfg, env(c.env)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestT(t *testing.T) {
	defer Set(Current())
	Set(ZH)
	if got := T("scope.label", "x"); got != "范围：x" {
		t.Errorf("zh: %q", got)
	}
	Set(EN)
	if got := T("scope.label", "x"); got != "Scope: x" {
		t.Errorf("en: %q", got)
	}
	if got := T("no.such.key"); got != "no.such.key" {
		t.Errorf("missing key: %q", got)
	}
}
