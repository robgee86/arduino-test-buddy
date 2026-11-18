#!/bin/sh
# SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Runs the arduino-test-buddy program of this plugin's version with the given arguments.
# The program is fetched from the GitHub release on first use and kept in the plugin's data folder.
# Nothing is ever printed on stdout before the program takes over: it carries the MCP protocol.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
home=${ARDUINO_TEST_BUDDY_HOME:?must name the folder that keeps the fetched program}
releases=${ARDUINO_TEST_BUDDY_RELEASES:-https://github.com/robgee86/arduino-test-buddy/releases/download}
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

fail() {
  echo "arduino-test-buddy: $1" >&2
  echo "arduino-test-buddy: could not fetch version $version for $os/$arch from $releases" >&2
  exit 1
}

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 2 -o "$2" "$1"
  else
    wget -q -O "$2" "$1"
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  else
    shasum -a 256 "$1" | cut -d ' ' -f 1
  fi
}

fetch() {
  archive=arduino-test-buddy_${version}_${os}_${arch}.tar.gz
  [ "$os" = windows ] && archive=arduino-test-buddy_${version}_${os}_${arch}.zip
  mkdir -p "$home"
  tmp=$(mktemp -d "$home/fetch.XXXXXX")
  trap 'rm -rf "$tmp"' EXIT
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
  chmod +x "$tmp/out/arduino-test-buddy$ext"
  mkdir -p "$home/$version"
  mv "$tmp/out/arduino-test-buddy$ext" "$program"
  rm -rf "$tmp"
  trap - EXIT
  # The versions an update left behind go, so the folder never grows; one still running elsewhere is left alone.
  for old in "$home"/*/; do
    [ "$old" = "$home/$version/" ] || rm -rf "$old" 2>/dev/null || true
  done
}

[ -x "$program" ] || fetch
exec "$program" "$@"
