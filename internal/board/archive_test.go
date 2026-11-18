// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// entry is one member of an archive as the board's tar would unpack it.
type entry struct {
	typeflag byte
	content  string
	link     string
	owner    string
}

func unpack(t *testing.T, data []byte) map[string]entry {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]entry{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		entries[hdr.Name] = entry{typeflag: hdr.Typeflag, content: string(content), link: hdr.Linkname, owner: hdr.Uname + hdr.Gname}
	}
}

func TestArchiveNamesEntriesAsTheBoardExpectsOnEveryPlatform(t *testing.T) {
	dir := t.TempDir()
	for path, content := range map[string]string{
		"app.yaml":                 "name: test\n",
		"python/main.py":           "print('hi')\n",
		"python/lib/with space.py": "x = 1\n",
		"assets/empty.txt":         "",
	} {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := archive(dir, &buf); err != nil {
		t.Fatal(err)
	}
	got := unpack(t, buf.Bytes())
	var names []string
	for name, e := range got {
		names = append(names, name)
		if e.owner != "" {
			t.Errorf("%s carries the owner %q of the developer machine", name, e.owner)
		}
	}
	slices.Sort(names)
	want := []string{"app.yaml", "assets/", "assets/empty.txt", "data/", "python/", "python/lib/", "python/lib/with space.py", "python/main.py"}
	if !slices.Equal(names, want) {
		t.Fatalf("entries must be relative, with forward slashes, folders included:\n got %q\nwant %q", names, want)
	}
	if got["python/lib/with space.py"].content != "x = 1\n" || got["app.yaml"].content != "name: test\n" || got["data/"].typeflag != tar.TypeDir {
		t.Errorf("contents and kinds must survive: %+v", got)
	}
}

func TestArchiveKeepsLinksAsLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("this machine cannot create links: %v", err)
	}
	var buf bytes.Buffer
	if err := archive(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if e := unpack(t, buf.Bytes())["link.txt"]; e.typeflag != tar.TypeSymlink || e.link != "target.txt" || e.content != "" {
		t.Errorf("a link must stay a link to the same target: %+v", e)
	}
}
