#!/bin/sh
# SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Runs the arduino-test-buddy program of this plugin's version with the given arguments.
# The program is fetched from the GitHub release on first use and kept in the plugin's data folder.
# When an update cannot be fetched, the version kept from before runs and the next start tries again.
# Nothing is ever printed on stdout before the program takes over: it carries the MCP protocol.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
home=${ARDUINO_TEST_BUDDY_HOME:?must name the folder that keeps the fetched program}
releases=${ARDUINO_TEST_BUDDY_RELEASES:-https://github.com/robgee86/arduino-test-buddy/releases/download}
# Claude Code gives a server 30 seconds to start: the fetch must end, or give way to a kept version, well before.
budget=${ARDUINO_TEST_BUDDY_FETCH_SECONDS:-20}
version=$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$root/.claude-plugin/plugin.json" | head -n 1)
[ -n "$version" ] || { echo "arduino-test-buddy: no version in $root/.claude-plugin/plugin.json" >&2; exit 1; }

ext=
case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  MINGW* | MSYS* | CYGWIN*) os=windows ext=.exe ;;
  *) echo "arduino-test-buddy: no release for $(uname -s)" >&2; exit 1 ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "arduino-test-buddy: no release for $(uname -m)" >&2; exit 1 ;;
esac
program=$home/$version/arduino-test-buddy$ext

# Each download gets what is left of the fetch's time. wget bounds only a stall, not the whole transfer.
download() {
  left=$((deadline - $(date +%s)))
  [ "$left" -gt 0 ] || return 1
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --max-time "$left" -o "$2" "$1"
  else
    wget -q -t 1 -T "$left" -O "$2" "$1"
  fi
}

# The file goes in on stdin: given a Windows path, the tools escape the name and mark the line with a backslash before the hash.
sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum < "$1" | cut -d ' ' -f 1
  else
    shasum -a 256 < "$1" | cut -d ' ' -f 1
  fi
}

# Downloads the archive of this version, checks it and places the program.
# It runs in a subshell: a failure says why, leaves nothing behind and ends only the fetch.
fetch() (
  tmp=
  fail() {
    echo "arduino-test-buddy: $1" >&2
    echo "arduino-test-buddy: could not fetch version $version for $os/$arch from $releases" >&2
    [ -z "$tmp" ] || rm -rf "$tmp"
    exit 1
  }
  archive=arduino-test-buddy_${version}_${os}_${arch}.tar.gz
  [ "$os" = windows ] && archive=arduino-test-buddy_${version}_${os}_${arch}.zip
  deadline=$(($(date +%s) + budget))
  mkdir -p "$home" && tmp=$(mktemp -d "$home/fetch.XXXXXX") || fail "cannot write in $home"
  download "$releases/v$version/$archive" "$tmp/$archive" || fail "download of $archive failed"
  download "$releases/v$version/checksums.txt" "$tmp/checksums.txt" || fail "download of checksums.txt failed"
  want=$(awk -v name="$archive" '$2 == name { print $1 }' "$tmp/checksums.txt")
  [ -n "$want" ] && [ "$(sha256 "$tmp/$archive")" = "$want" ] || fail "$archive does not match its checksum"
  if [ "$os" = windows ]; then
    unzip -q "$tmp/$archive" -d "$tmp/out" || fail "could not unpack $archive"
  else
    mkdir "$tmp/out" && tar -xzf "$tmp/$archive" -C "$tmp/out" || fail "could not unpack $archive"
  fi
  [ -f "$tmp/out/arduino-test-buddy$ext" ] || fail "$archive holds no arduino-test-buddy$ext"
  chmod +x "$tmp/out/arduino-test-buddy$ext" && mkdir -p "$home/$version" || fail "cannot write in $home"
  # Another session may have fetched the same version meanwhile, and Windows cannot replace a program that runs: either way it is there.
  mv "$tmp/out/arduino-test-buddy$ext" "$program" 2>/dev/null || [ -x "$program" ] || fail "could not place $program"
  rm -rf "$tmp"
  # The versions an update left behind go, so the folder never grows. Only a folder holding the program is one:
  # the folder of another session's fetch in progress and anything else are not touched.
  for old in "$home"/*/; do
    [ "$old" != "$home/$version/" ] && [ -f "${old}arduino-test-buddy$ext" ] || continue
    rm -rf "$old" 2>/dev/null || true
  done
)

# Names the newest version whose program is still in the data folder.
kept() {
  for old in "$home"/*/; do
    [ -x "${old}arduino-test-buddy$ext" ] && basename "$old"
  done | sort -t . -k 1,1n -k 2,2n -k 3,3n | tail -n 1
}

# After a failed fetch the program may still be there, placed by another session.
if [ ! -x "$program" ] && ! fetch && [ ! -x "$program" ]; then
  other=$(kept)
  [ -n "$other" ] || exit 1
  echo "arduino-test-buddy: running version $other, kept from before the update; the next start tries again" >&2
  program=$home/$other/arduino-test-buddy$ext
fi
exec "$program" "$@"
