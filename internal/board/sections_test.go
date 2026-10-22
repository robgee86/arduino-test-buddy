// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"os/exec"
	"testing"
)

func TestSectionsSurviveOutputWithoutTrailingNewline(t *testing.T) {
	script := sectioned(map[string]string{
		"lsm":  "printf capability,landlock",
		"next": "echo after",
	}, []string{"lsm", "next"})
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := parseSections(string(out))
	if s["lsm"] != "capability,landlock" || s["next"] != "after" {
		t.Errorf("sections: %q", s)
	}
}
