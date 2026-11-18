// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import "os/exec"

// endWithChildren changes nothing on Windows: a cancel ends the command alone, and what it started runs on.
func endWithChildren(*exec.Cmd) {}
