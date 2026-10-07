// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package shell

import "testing"

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"":                   "''",
		"plain-word_1.0/x:y": "plain-word_1.0/x:y",
		"has space":          "'has space'",
		"it's":               `'it'\''s'`,
		"$HOME":              "'$HOME'",
		"a|b":                "'a|b'",
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestJoin(t *testing.T) {
	got := Join("docker", "exec", "c", "sh", "-c", "echo hi; ls")
	want := "docker exec c sh -c 'echo hi; ls'"
	if got != want {
		t.Errorf("Join = %s, want %s", got, want)
	}
}
