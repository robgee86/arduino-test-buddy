// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"strings"

	"github.com/arduino/arduino-test-buddy/internal/shell"
)

// sectionMarker separates the outputs of several commands run in one round trip.
const sectionMarker = "@@"

// sectioned prefixes every command with a marker line so one script yields named outputs.
func sectioned(commands map[string]string, order []string) string {
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, shell.Join("echo", sectionMarker+name), commands[name]+" 2>/dev/null || true")
	}
	return shell.Script(parts...)
}

// parseSections splits a sectioned output back into the named outputs.
func parseSections(out string) map[string]string {
	sections := map[string]string{}
	current := ""
	var buf []string
	flush := func() {
		if current != "" {
			sections[current] = strings.TrimRight(strings.Join(buf, "\n"), "\n")
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, sectionMarker) {
			flush()
			current = strings.TrimPrefix(line, sectionMarker)
			buf = nil
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return sections
}

// lines splits a section into its non-empty lines.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
