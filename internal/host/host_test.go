// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"context"
	"errors"
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
	case strings.Contains(path, "/manifests/") && (req.Method == http.MethodHead || req.Method == http.MethodGet):
		repo, tag, _ := strings.Cut(path, "/manifests/")
		if !slices.Contains(r.tags[repo], tag) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:"+tag)
		w.WriteHeader(http.StatusOK)
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

// pushedAgo writes the file the registry keeps for repo:tag, dated as if its last push was age ago.
func pushedAgo(t *testing.T, h *Host, repo, tag string, age time.Duration) {
	link := h.tagFile(repo, tag)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("sha256:x"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(link, when, when); err != nil {
		t.Fatal(err)
	}
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

// configure writes the developer's config file for h.
func configure(t *testing.T, h *Host, content string) {
	h.ConfigFile = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(h.ConfigFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// built pushes without naming a source from the folder cwd and returns the checkout that was built, as a real path.
func built(t *testing.T, h *Host, cwd string, req PushRequest) (string, error) {
	t.Chdir(cwd)
	req.SkipWheel, req.Targets = true, []string{"tps"}
	res, err := h.Push(context.Background(), req)
	if err != nil {
		return "", err
	}
	return realPath(t, res.Source), nil
}

func realPath(t *testing.T, path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestPushBuildsTheCheckoutAskedForTheCurrentOneOrTheConfiguredOneInThatOrder(t *testing.T) {
	h, _, _ := newHost(t, map[string][]string{"app-bricks/tps": {"main"}})
	asked, current, configured, elsewhere := checkout(t, "main"), checkout(t, "main"), checkout(t, "main"), t.TempDir()
	configure(t, h, "source: "+configured+"\n")

	if got, err := built(t, h, current, PushRequest{Source: asked}); err != nil || got != realPath(t, asked) {
		t.Errorf("the checkout asked for must win over the current and the configured one: %q %v", got, err)
	}
	if got, err := built(t, h, current, PushRequest{}); err != nil || got != realPath(t, current) {
		t.Errorf("inside a checkout, a worktree included, that checkout is built whatever the config says: %q %v", got, err)
	}
	if got, err := built(t, h, elsewhere, PushRequest{}); err != nil || got != realPath(t, configured) {
		t.Errorf("outside any checkout the configured one is built: %q %v", got, err)
	}
}

func TestPushReadsTheConfiguredCheckoutUnderTheHomeFolder(t *testing.T) {
	h, _, _ := newHost(t, map[string][]string{"app-bricks/tps": {"main"}})
	home := checkout(t, "main")
	t.Setenv("HOME", filepath.Dir(home))
	t.Setenv("USERPROFILE", filepath.Dir(home))
	configure(t, h, "source: ~/"+filepath.Base(home)+"\n")
	if got, err := built(t, h, t.TempDir(), PushRequest{}); err != nil || got != realPath(t, home) {
		t.Errorf("a leading ~ is the home folder: %q %v", got, err)
	}
}

func TestPushSaysWhereACheckoutCanComeFromWhenItFindsNone(t *testing.T) {
	h, cmd, _ := newHost(t, map[string][]string{})
	elsewhere := t.TempDir()
	for name, tc := range map[string]struct {
		config string
		req    PushRequest
		want   string
	}{
		"nothing given, no config":          {"", PushRequest{}, "set `source` in"},
		"an empty config":                   {"\n", PushRequest{}, "set `source` in"},
		"a configured folder with no build": {"source: " + elsewhere + "\n", PushRequest{}, "the source set in"},
		"a misspelled key":                  {"sorce: " + elsewhere + "\n", PushRequest{}, "sorce"},
		"an asked folder with no build":     {"", PushRequest{Source: elsewhere}, "is not an app-bricks-py checkout"},
	} {
		h.ConfigFile = ""
		if tc.config != "" {
			configure(t, h, tc.config)
		}
		if _, err := built(t, h, elsewhere, tc.req); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error naming %q, got %v", name, tc.want, err)
		}
	}
	if cmd.ran("task ") {
		t.Error("nothing may build without a checkout")
	}
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
	pushedAgo(t, h, "app-bricks/tps", "main", 30*24*time.Hour)
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
	if _, _, err := h.Use(context.Background()); err != ErrDown {
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
	if !cmd.ran("docker run -d --name arduino-test-buddy-registry") || cmd.ran("--restart unless-stopped") {
		t.Error("the registry must be created without a restart policy")
	}
	// Windows has no user id to run the registry as
	if hasUser := os.Getuid() >= 0; cmd.ran("--user") != hasUser {
		t.Errorf("the registry must run as the user exactly where the system has user ids:\n%s", strings.Join(cmd.commands, "\n"))
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
		pushedAgo(t, h, tc.repo, tc.tag, tc.age)
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
	_, release, err := h.Use(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.lock(true, false); err != errBusy {
		t.Errorf("a down must not get the lock while a run holds the registry: %v", err)
	}
	release()
	free, err := h.lock(true, false)
	if err != nil {
		t.Fatalf("the lock must be free once the run released it: %v", err)
	}
	free.release()
}

func TestUpDropsTheTagsNoPushUsedFor3DaysAndKeepsTheOthers(t *testing.T) {
	h, cmd, reg := newHost(t, map[string][]string{
		"app-bricks/python-apps-base": {"fresh", "forgotten"},
		"app-bricks/tps":              {"forgotten"},
	})
	pushedAgo(t, h, "app-bricks/python-apps-base", "fresh", 71*time.Hour)
	pushedAgo(t, h, "app-bricks/python-apps-base", "forgotten", 73*time.Hour)
	pushedAgo(t, h, "app-bricks/tps", "forgotten", 30*24*time.Hour)
	st, err := h.Up(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(reg.deleted)
	if !slices.Equal(reg.deleted, []string{"app-bricks/python-apps-base:forgotten", "app-bricks/tps:forgotten"}) || !slices.Equal(st.Dropped, []string{"forgotten"}) {
		t.Errorf("up must drop the forgotten tag from every repository and nothing else: deleted %v, reported %v", reg.deleted, st.Dropped)
	}
	if !cmd.ran("registry garbage-collect --delete-untagged") || !cmd.ran("docker restart arduino-test-buddy-registry") {
		t.Errorf("the layers of a dropped tag must be collected:\n%s", strings.Join(cmd.commands, "\n"))
	}
}

func TestUpLeavesTheRegistryAloneWhenNoTagIsStale(t *testing.T) {
	h, cmd, reg := newHost(t, map[string][]string{"app-bricks/tps": {"fresh"}})
	pushedAgo(t, h, "app-bricks/tps", "fresh", time.Hour)
	st, err := h.Up(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.deleted) != 0 || len(st.Dropped) != 0 || cmd.ran("garbage-collect") || cmd.ran("docker restart") {
		t.Errorf("nothing stale means nothing deleted, collected or restarted: %v %v\n%s", reg.deleted, st.Dropped, strings.Join(cmd.commands, "\n"))
	}
}

func TestDownDropsStaleTagsBeforeItStopsTheRegistry(t *testing.T) {
	h, cmd, reg := newHost(t, map[string][]string{"app-bricks/tps": {"forgotten"}})
	pushedAgo(t, h, "app-bricks/tps", "forgotten", 30*24*time.Hour)
	st, err := h.Down(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	collect := slices.IndexFunc(cmd.commands, func(c string) bool { return strings.Contains(c, "garbage-collect") })
	stop := slices.IndexFunc(cmd.commands, func(c string) bool { return strings.Contains(c, "docker stop arduino-test-buddy-registry") })
	if !slices.Equal(reg.deleted, []string{"app-bricks/tps:forgotten"}) || !slices.Equal(st.Dropped, []string{"forgotten"}) || collect < 0 || collect > stop {
		t.Errorf("down must drop the stale tag while the registry still runs: %v %v\n%s", reg.deleted, st.Dropped, strings.Join(cmd.commands, "\n"))
	}
}

func TestFullPushDropsStaleTagsButNeverTheOneItPushed(t *testing.T) {
	h, _, reg := newHost(t, map[string][]string{"app-bricks/python-apps-base": {"feature-x", "forgotten"}})
	// The registry rewrites a tag file only when its content changes: an unchanged rebuild leaves it old
	pushedAgo(t, h, "app-bricks/python-apps-base", "feature-x", 30*24*time.Hour)
	pushedAgo(t, h, "app-bricks/python-apps-base", "forgotten", 30*24*time.Hour)
	res, err := h.Push(context.Background(), PushRequest{Source: checkout(t, "feature/x"), SkipWheel: true})
	if err != nil || res.Error != "" {
		t.Fatalf("push: %+v %v", res, err)
	}
	if !slices.Equal(reg.deleted, []string{"app-bricks/python-apps-base:forgotten"}) {
		t.Errorf("only the forgotten tag may go: %v", reg.deleted)
	}
}

func TestNarrowedPushKeepsStaleTags(t *testing.T) {
	h, _, reg := newHost(t, map[string][]string{"app-bricks/tps": {"main", "forgotten"}})
	pushedAgo(t, h, "app-bricks/tps", "forgotten", 30*24*time.Hour)
	if _, err := h.Push(context.Background(), PushRequest{Source: checkout(t, "main"), Targets: []string{"tps"}, SkipWheel: true}); err != nil {
		t.Fatal(err)
	}
	if len(reg.deleted) != 0 {
		t.Errorf("a narrowed push prunes nothing: %v", reg.deleted)
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
	h, cmd, reg := newHost(t, map[string][]string{"app-bricks/tps": {"forgotten"}})
	shared, err := h.lock(false, true)
	if err != nil {
		t.Fatal(err)
	}
	pushedAgo(t, h, "app-bricks/tps", "forgotten", 30*24*time.Hour)
	steps, dropped := h.pruneUnused(context.Background())
	if len(steps) != 1 || !strings.Contains(steps[0].Name, "skipped") || len(dropped) != 0 || len(reg.deleted) != 0 || cmd.ran("buildx prune") {
		t.Errorf("pruning during another push must be skipped, cache and tags alike: %+v %v %v", steps, dropped, reg.deleted)
	}
	shared.release()
	free, err := h.lock(true, false)
	if err != nil {
		t.Fatalf("the lock must be free once the push released it: %v", err)
	}
	free.release()
}

func TestPushesShareTheCacheAndAPruneTakesItAlone(t *testing.T) {
	h, _, _ := newHost(t, map[string][]string{})
	first, err := h.lock(false, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.lock(false, false)
	if err != nil {
		t.Fatalf("two pushes must hold the cache together: %v", err)
	}
	if _, err := h.lock(true, false); err != errBusy {
		t.Errorf("a prune must find the cache busy while pushes hold it: %v", err)
	}
	// A waiting prune gets the cache as soon as the last push lets go
	got := make(chan error, 1)
	go func() {
		alone, err := h.lock(true, true)
		if err == nil {
			defer alone.release()
			if _, busy := h.lock(false, false); busy != errBusy {
				err = errors.New("a push got the cache while a prune held it alone")
			}
		}
		got <- err
	}()
	first.release()
	select {
	case err := <-got:
		t.Fatalf("the prune must wait for every push, it ended with %v after the first one", err)
	case <-time.After(200 * time.Millisecond):
	}
	second.release()
	select {
	case err := <-got:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting prune never got the cache")
	}
}

func TestBoardsReachTheRegistryReadOnly(t *testing.T) {
	h, _, reg := newHost(t, map[string][]string{"app-bricks/tps": {"bt-x"}})
	port, release, err := h.Use(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	base := "http://127.0.0.1:" + port
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, _ := http.NewRequest(method, base+"/v2/app-bricks/tps/tags/list", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s through the tunnel end: %v %v", method, resp, err)
		}
		resp.Body.Close()
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(method, base+"/v2/app-bricks/tps/manifests/bt-x", strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "GET, HEAD" {
			t.Fatalf("%s through the tunnel end must be refused: %v %v", method, resp, err)
		}
		resp.Body.Close()
	}
	if len(reg.deleted) != 0 {
		t.Errorf("a refused delete must never reach the registry: %v", reg.deleted)
	}
	release()
	if _, err := http.Get(base + "/v2/"); err == nil {
		t.Error("the tunnel end must close with the turn")
	}
}

func TestDigestIsTheManifestTheRegistryServesForTheTag(t *testing.T) {
	h, _, _ := newHost(t, map[string][]string{"app-bricks/tps": {"bt-x"}})
	if d, err := h.Digest(context.Background(), "app-bricks/tps:bt-x"); err != nil || d != "sha256:bt-x" {
		t.Errorf("digest of a pushed tag: %q %v", d, err)
	}
	if d, err := h.Digest(context.Background(), "app-bricks/tps:gone"); err != nil || d != "" {
		t.Errorf("a tag the registry does not hold has no digest: %q %v", d, err)
	}
}
