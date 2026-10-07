// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package version holds the build version, set by the linker at release time.
package version

// Version is overridden with -ldflags "-X .../internal/version.Version=<tag>".
var Version = "0.0.0-dev"
