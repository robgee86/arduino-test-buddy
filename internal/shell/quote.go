// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package shell builds POSIX shell command lines without hand escaping.
package shell

import "strings"

// Quote returns s as one shell word, single-quoted unless it is already safe.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if isSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Join quotes every word and joins them with spaces.
func Join(words ...string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = Quote(w)
	}
	return strings.Join(quoted, " ")
}

// Script joins commands with "; " so they run in sequence on the board.
func Script(commands ...string) string {
	return strings.Join(commands, "; ")
}

func isSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:=,+@%", r):
		default:
			return false
		}
	}
	return true
}
