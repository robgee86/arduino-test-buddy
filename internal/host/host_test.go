// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCommander records every command and answers "the registry runs, the builder exists" unless told otherwise.
type fakeCommander struct {
	mu       sync.Mutex
	commands []string
	answers  map[string]result
}

type result struct {
	out  string
	code int
}

func (f *fakeCommander) Run(_ context.Context, _ string, env []string, name string, args ...string) (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.TrimSpace(strings.Join(env, " ") + " " + name + " " + strings.Join(args, " "))
	f.commands = append(f.commands, line)
	for prefix, r := range f.answers {
		if strings.Contains(line, prefix) {
			return r.out, r.code
		}
	}
	switch {
	case strings.HasPrefix(line, "docker ps"):
		return "running\n", 0
	case strings.HasPrefix(line, "docker buildx inspect "+BuilderName):
		return "Name: " + BuilderName + "\nNodes:\nStatus: running\n", 0
	}
	return "", 0
}

// down makes the fake report both containers stopped.
func (f *fakeCommander) down() {
	f.answers["docker ps"] = result{out: "exited\n"}
	f.answers["docker buildx inspect "+BuilderName] = result{out: "Status: stopped\n"}
}

func (f *fakeCommander) ran(substring string) bool {
	return slices.ContainsFunc(f.commands, func(c string) bool { return strings.Contains(c, substring) })
}

// fakeRegistry serves a catalog of repo -> tags and records deletes.
type fakeRegistry struct {
	mu      sync.Mutex
	tags    map[string][]string
	deleted []string
}

func (r *fakeRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := strings.TrimPrefix(req.URL.Path, "/v2/")
	switch {
	case req.URL.Path == "/v2/":
		w.WriteHeader(http.StatusOK)
	case path == "_catalog":
		var repos []string
		for repo := range r.tags {
			repos = append(repos, `"`+repo+`"`)
		}
		slices.Sort(repos)
		w.Write([]byte(`{"repositories":[` + strings.Join(repos, ",") + `]}`))
	case strings.HasSuffix(path, "/tags/list"):
		repo := strings.TrimSuffix(path, "/tags/list")
		var quoted []string
		for _, t := range r.tags[repo] {
			quoted = append(quoted, `"`+t+`"`)
		}
		w.Write([]byte(`{"tags":[` + strings.Join(quoted, ",") + `]}`))
	case req.Method == http.MethodDelete:
		repo, tag, _ := strings.Cut(path, "/manifests/")
		r.deleted = append(r.deleted, repo+":"+tag)
		r.tags[repo] = slices.DeleteFunc(r.tags[repo], func(t string) bool { return t == tag })
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newHost(t *testing.T, tags map[string][]string) (*Host, *fakeCommander, *fakeRegistry) {
	reg := &fakeRegistry{tags: tags}
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	cmd := &fakeCommander{answers: map[string]result{}}
	return &Host{Dir: t.TempDir(), url: srv.URL, cmd: cmd}, cmd, reg
}

// checkout makes a git repository that looks like app-bricks-py, on a branch.
func checkout(t *testing.T, branch string) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-bake.hcl"), []byte("group \"default\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", branch}, {"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return dir
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"object-tracking":     "object-tracking",
		"feature/Foo_Bar baz": "feature-foo_bar-baz",
		"--weird--":           "weird",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFullPushBuildsWithTheToolBuilderAndDropsStaleCache(t *testing.T) {
	h, cmd, _ := newHost(t, map[string][]string{"app-bricks/python-apps-base": {"feature-x", "other"}})
	res, err := h.Push(context.Background(), PushRequest{Source: checkout(t, "feature/x")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" || res.Tag != "feature-x" || !slices.Equal(res.Images, []string{"app-bricks/python-apps-base:feature-x"}) {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !cmd.ran("BUILDX_BUILDER=arduino-test-buddy REGISTRY=localhost:5005/ IMAGE_TAG=feature-x BASE_IMAGE_VERSION=feature-x") ||
		!cmd.ran("task build:containers PUSH=1 --") {
		t.Errorf("the repository task must build on the tool's builder and push:\n%s", strings.Join(cmd.commands, "\n"))
	}
	if !cmd.ran("docker buildx prune --builder arduino-test-buddy --force --filter until=72h") {
		t.Error("a full push must drop the cache no push used for 3 days, and keep what parallel branches still use")
	}
	if cmd.ran("buildx create") {
		t.Error("an existing builder must be reused")
	}
}

func TestPushDatesTheTagEvenWhenNothingChanged(t *testing.T) {
	h, _, _ := newHost(t, map[string][]string{"app-bricks/tps": {"main"}})
	link := h.tagFile("app-bricks/tps", "main")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.WriteFile(link, []byte("sha256:x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(link, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Push(context.Background(), PushRequest{Source: checkout(t, "main"), Targets: []string{"tps"}, SkipWheel: true}); err != nil {
		t.Fatal(err)
	}
	st, err := h.Status(context.Background())
	if err != nil || len(st.Tags) != 1 || st.Tags[0].Stale(time.Now()) {
		t.Errorf("a pushed tag must not look stale: %+v %v", st.Tags, err)
	}
}

func TestNarrowedPushKeepsTheCache(t *testing.T) {
	h, cmd, _ := newHost(t, map[string][]string{"app-bricks/tps": {"main"}})
	res, err := h.Push(context.Background(), PushRequest{Source: checkout(t, "main"), Targets: []string{"tps"}, SkipWheel: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" || !cmd.ran("task build:containers PUSH=1 -- tps") {
		t.Fatalf("unexpected result: %+v\n%s", res, strings.Join(cmd.commands, "\n"))
	}
	if cmd.ran("buildx prune") || cmd.ran("build:bricks") {
		t.Error("a narrowed push must neither prune the cache nor rebuild a skipped wheel")
	}
}

func TestNothingStartsByItselfWhileDown(t *testing.T) {
	h, cmd, _ := newHost(t, map[string][]string{})
	cmd.down()
	if _, err := h.Push(context.Background(), PushRequest{Source: checkout(t, "main")}); err != ErrDown {
		t.Errorf("push while down: %v", err)
	}
	if _, err := h.Prune(context.Background(), PruneRequest{Tag: "x"}); err != ErrDown {
		t.Errorf("prune of a tag while down: %v", err)
	}
	if _, err := h.Use(context.Background()); err != ErrDown {
		t.Errorf("a board run while down: %v", err)
	}
	st, err := h.Status(context.Background())
	if err != nil || st.Up() || st.Registry != Stopped || st.Builder != Stopped {
		t.Errorf("status: %+v %v", st, err)
	}
	if cmd.ran("docker start") || cmd.ran("--bootstrap") || cmd.ran("task ") || cmd.ran("buildx du") || cmd.ran("buildx prune") {
		t.Errorf("nothing may start or build while down:\n%s", strings.Join(cmd.commands, "\n"))
	}
}

func TestUpCreatesWhatIsMissingWithoutAutomaticRestarts(t *testing.T) {
	h, cmd, _ := newHost(t, map[string][]string{})
	cmd.answers["docker ps"] = result{}
	cmd.answers["docker buildx inspect "+BuilderName] = result{code: 1}
	if _, err := h.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !cmd.ran("docker run -d --name arduino-test-buddy-registry --user") || cmd.ran("--restart unless-stopped") {
		t.Error("the registry must be created without a restart policy")
	}
	if !cmd.ran("docker buildx create --name arduino-test-buddy --driver docker-container --driver-opt network=host --bootstrap") {
		t.Error("a missing builder must be created with host networking")
	}
	if !cmd.ran("docker update --restart=no arduino-test-buddy-registry buildx_buildkit_arduino-test-buddy0") {
		t.Error("neither container may restart with Docker")
	}
}

func TestUpStartsStoppedContainersAndDownStopsThem(t *testing.T) {
	h, cmd, _ := newHost(t, map[string][]string{})
	cmd.down()
	if _, err := h.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !cmd.ran("docker start arduino-test-buddy-registry") || !cmd.ran("docker buildx inspect --bootstrap arduino-test-buddy") || cmd.ran("docker run") {
		t.Errorf("up must start, not recreate:\n%s", strings.Join(cmd.commands, "\n"))
	}
	if !cmd.ran("docker buildx prune --builder arduino-test-buddy --force --filter until=72h") {
		t.Error("up must drop build cache no push used for 3 days")
	}
	delete(cmd.answers, "docker ps")
	delete(cmd.answers, "docker buildx inspect "+BuilderName)
	cmd.commands = nil
	if _, err := h.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	trim := slices.IndexFunc(cmd.commands, func(c string) bool { return strings.Contains(c, "buildx prune") })
	stop := slices.IndexFunc(cmd.commands, func(c string) bool { return strings.Contains(c, "docker buildx stop arduino-test-buddy") })
	if trim < 0 || stop < 0 || trim > stop || !cmd.ran("docker stop arduino-test-buddy-registry") {
		t.Errorf("down must trim the cache, then stop both:\n%s", strings.Join(cmd.commands, "\n"))
	}
}

func TestStatusListsTagsOldestFirstFromTheFolderEvenWhileDown(t *testing.T) {
	h, cmd, _ := newHost(t, map[string][]string{})
	cmd.down()
	now := time.Now()
	for _, tc := range []struct {
		repo, tag string
		age       time.Duration
	}{
		{"app-bricks/python-apps-base", "fresh", time.Hour},
		{"app-bricks/tps", "fresh", 2 * time.Hour},
		{"app-bricks/python-apps-base", "forgotten", 30 * 24 * time.Hour},
	} {
		link := filepath.Join(h.RegistryDir(), "docker/registry/v2/repositories", tc.repo, "_manifests/tags", tc.tag, "current/link")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(link, []byte("sha256:x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(link, now.Add(-tc.age), now.Add(-tc.age)); err != nil {
			t.Fatal(err)
		}
	}
	st, err := h.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Tags) != 2 || st.Tags[0].Name != "forgotten" || !st.Tags[0].Stale(now) {
		t.Fatalf("the forgotten tag must come first and be stale: %+v", st.Tags)
	}
	fresh := st.Tags[1]
	if fresh.Stale(now) || !slices.Equal(fresh.Images, []string{"app-bricks/python-apps-base", "app-bricks/tps"}) || now.Sub(fresh.Pushed) > 90*time.Minute {
		t.Errorf("a tag's push time is its newest image's: %+v", fresh)
	}
}

func TestDownWaitsForRunsHoldingTheRegistry(t *testing.T) {
	h, _, _ := newHost(t, map[string][]string{})
	release, err := h.Use(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.lock(true, false); err != errBusy {
		t.Errorf("a down must not get the lock while a run holds the registry: %v", err)
	}
	release()
	if _, err := h.lock(true, false); err != nil {
		t.Errorf("the lock must be free once the run released it: %v", err)
	}
}

func TestPushReportsAFailedBuildAndNothingPushed(t *testing.T) {
	h, cmd, _ := newHost(t, map[string][]string{})
	cmd.answers["build:containers"] = result{out: "ERROR: boom", code: 1}
	res, err := h.Push(context.Background(), PushRequest{Source: checkout(t, "main"), SkipWheel: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "build failed" || cmd.ran("buildx prune") {
		t.Errorf("a failed build must stop before pruning: %+v", res)
	}
}

func TestPruneTagDeletesOnlyThatTagThenCollects(t *testing.T) {
	h, cmd, reg := newHost(t, map[string][]string{
		"app-bricks/python-apps-base": {"a", "b"},
		"app-bricks/tps":              {"a"},
	})
	res, err := h.Prune(context.Background(), PruneRequest{Tag: "a"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(reg.deleted)
	if !slices.Equal(reg.deleted, []string{"app-bricks/python-apps-base:a", "app-bricks/tps:a"}) {
		t.Errorf("deleted %v", reg.deleted)
	}
	if !cmd.ran("docker exec arduino-test-buddy-registry registry garbage-collect --delete-untagged") {
		t.Error("deleting a tag must collect the layers it freed")
	}
	if !cmd.ran("docker restart arduino-test-buddy-registry") {
		t.Error("the registry must restart after collecting, or its cache keeps claiming deleted layers")
	}
	if cmd.ran("buildx prune") || len(res.Steps) != 4 {
		t.Errorf("only the tag was asked: %+v", res.Steps)
	}
}

func TestPruneAllRemovesTheFolderTheRegistryAndTheBuilder(t *testing.T) {
	h, cmd, _ := newHost(t, map[string][]string{})
	if err := os.MkdirAll(filepath.Join(h.RegistryDir(), "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Prune(context.Background(), PruneRequest{All: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.Dir); !os.IsNotExist(err) {
		t.Error("the whole cache folder, lock file included, must be gone")
	}
	if !cmd.ran("docker rm -f arduino-test-buddy-registry") || !cmd.ran("docker buildx rm arduino-test-buddy") {
		t.Errorf("container and builder must be removed:\n%s", strings.Join(cmd.commands, "\n"))
	}
}

func TestPruneNeedsSomethingToPrune(t *testing.T) {
	h, _, _ := newHost(t, map[string][]string{})
	if _, err := h.Prune(context.Background(), PruneRequest{}); err == nil {
		t.Error("an empty request must be refused")
	}
}

func TestPruneWaitsForRunningPushesAndAPushSkipsPruningDuringOne(t *testing.T) {
	h, _, _ := newHost(t, map[string][]string{})
	shared, err := h.lock(false, true)
	if err != nil {
		t.Fatal(err)
	}
	if step := h.pruneUnused(context.Background()); !strings.Contains(step.Name, "skipped") {
		t.Errorf("pruning during another push must be skipped: %+v", step)
	}
	shared.release()
	if _, err := h.lock(true, false); err != nil {
		t.Errorf("the lock must be free once the push released it: %v", err)
	}
}
