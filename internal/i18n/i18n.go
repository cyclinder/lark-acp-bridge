// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package i18n localizes the bridge's bot-facing output (slash-command
// replies, card text). The English literal is the message key and the
// built-in default; SetLocale("zh") switches lookups to the Chinese table.
// Unknown keys fall back to the English literal, so a missing translation
// degrades gracefully instead of failing.
//
// The locale is set once at startup (from config `language` or the
// LARK_ACP_BRIDGE_LANG environment variable) before any message is served,
// so no synchronization is needed on the read path.
package i18n

import "fmt"

// Locales supported by the bridge.
const (
	LocaleEN = "en"
	LocaleZH = "zh"
)

var locale = LocaleEN

// SetLocale selects the output language. Returns an error for unsupported
// locales so config validation can surface it at startup.
func SetLocale(l string) error {
	switch l {
	case "", LocaleEN:
		locale = LocaleEN
	case LocaleZH:
		locale = LocaleZH
	default:
		return fmt.Errorf("unsupported language %q (supported: en, zh)", l)
	}
	return nil
}

// Locale returns the active locale.
func Locale() string { return locale }

// T translates a message. The English literal is the key; when the active
// locale has no entry the key itself is returned.
func T(msg string) string {
	if locale == LocaleZH {
		if t, ok := zh[msg]; ok {
			return t
		}
	}
	return msg
}
