// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"regexp"
	"testing"
)

func TestSetLocale(t *testing.T) {
	t.Cleanup(func() { locale = LocaleEN })
	if err := SetLocale("zh"); err != nil || Locale() != LocaleZH {
		t.Fatalf("SetLocale(zh): err=%v locale=%s", err, Locale())
	}
	if err := SetLocale(""); err != nil || Locale() != LocaleEN {
		t.Fatalf("SetLocale(\"\"): err=%v locale=%s", err, Locale())
	}
	if err := SetLocale("fr"); err == nil {
		t.Fatal("SetLocale(fr): expected error")
	}
}

func TestTranslate(t *testing.T) {
	t.Cleanup(func() { locale = LocaleEN })
	if got := T("Session cleared."); got != "Session cleared." {
		t.Fatalf("en: got %q", got)
	}
	if err := SetLocale(LocaleZH); err != nil {
		t.Fatal(err)
	}
	if got := T("Session cleared."); got != "会话已清除。" {
		t.Fatalf("zh: got %q", got)
	}
	// A key missing from the zh table falls back to the English literal.
	if got := T("not a real key"); got != "not a real key" {
		t.Fatalf("fallback: got %q", got)
	}
}

// TestFormatVerbParity ensures every zh translation carries the same format
// verbs, in the same order, as its English key, so fmt.Sprintf on either
// locale consumes arguments identically.
func TestFormatVerbParity(t *testing.T) {
	verbRe := regexp.MustCompile(`%[-+#0-9.]*[a-zA-Z]`)
	for key, val := range zh {
		kv := verbRe.FindAllString(key, -1)
		vv := verbRe.FindAllString(val, -1)
		if len(kv) != len(vv) {
			t.Errorf("verb count mismatch for %q: en=%v zh=%v", key, kv, vv)
			continue
		}
		for i := range kv {
			if kv[i] != vv[i] {
				t.Errorf("verb mismatch for %q: en=%v zh=%v", key, kv, vv)
				break
			}
		}
	}
}
