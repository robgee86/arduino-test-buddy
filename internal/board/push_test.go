// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"object-tracking":        "object-tracking",
		"feature/Foo_Bar baz":    "feature-foo_bar-baz",
		"release/1.2.0":          "release-1.2.0",
		"--weird--":              "weird",
		"ci-unify-release-proc.": "ci-unify-release-proc.",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaggedRefsKeepsOnlyThePushPrefixAndTag(t *testing.T) {
	refs := []string{
		"localhost:51527/app-bricks/python-apps-base:bt-lean",
		"localhost:51527/app-bricks/tps:bt-lean",
		"localhost:51527/app-bricks/tps:bt-other",
		"localhost:62758/app-bricks/python-apps-base:bt-lean",
		"ghcr.io/arduino/app-bricks/python-apps-base:bt-lean",
		"",
	}
	got := taggedRefs(refs, "localhost:51527/", "bt-lean")
	want := []string{"localhost:51527/app-bricks/python-apps-base:bt-lean", "localhost:51527/app-bricks/tps:bt-lean"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRegistryDeleteScriptTargetsOneTag(t *testing.T) {
	script := registryDeleteScript("app-bricks/python-apps-base:bt-x")
	for _, want := range []string{"/v2/app-bricks/python-apps-base/manifests/bt-x", "-X DELETE"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}

func TestExamplesDefaultToTheStartupMarker(t *testing.T) {
	f := (&fakeRunner{}).
		on(`test -d .*/sketch`, "").
		onExit(`test -d .*/sketch`, 1, "").
		on(`app start examples:`, strings.Replace(startedEvent, "bt-probe", "x", 1)).
		on(`app logs examples:`, "@@logs\n[main] ======== App is starting ====\n[main] 2026 INFO - App:  App started\n@@state\nrunning\n")
	f.answers = f.answers[1:] // the first answer would report a sketch; keep only the "no sketch" one
	res, err := New("x", f, Dev{}).Run(context.Background(), RunRequest{App: "examples:bricks/arduino/weather_forecast/01_city"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Marker != ExampleMarker || !res.MarkerFound {
		t.Errorf("expected the startup marker to end the run: %+v", res)
	}
}

func TestRunStopsPollingWhenTheContainerExited(t *testing.T) {
	f := (&fakeRunner{}).
		on(`app start .*bt-crash`, strings.Replace(startedEvent, "bt-probe", "bt-crash", 1)).
		on(`app logs .*bt-crash`, "@@logs\n[main] ======== App is starting ====\n[main] Traceback: boom\n@@state\nexited\n")
	res, err := New("x", f, Dev{}).Run(context.Background(), RunRequest{App: "bt-crash", TimeoutS: 600})
	if err != nil {
		t.Fatal(err)
	}
	if res.MarkerFound || res.TimedOut || !strings.Contains(res.Error, "exited") || !strings.Contains(res.Log, "boom") {
		t.Errorf("expected an early exit with the traceback: %+v", res)
	}
}

func TestCleanupDeletesTheTagFromTheRegistry(t *testing.T) {
	inventory := "@@apps\n" + `{"apps":[]}` + "\n@@containers\n@@networks\n@@images\n@@assets\n"
	f := (&fakeRunner{}).
		on(`@@apps`, inventory).
		on(`_catalog`, `{"repositories":["app-bricks/python-apps-base","app-bricks/models-downloader"]}`).
		on(`tags/list`, "@@app-bricks/python-apps-base\n"+`{"tags":["bt-x","bt-other"]}`+"\n@@app-bricks/models-downloader\n"+`{"tags":["bt-other"]}`+"\n")
	report, err := New("x", f, Dev{Tag: "bt-x"}).Cleanup(context.Background(), CleanupRequest{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Steps) != 1 || !strings.Contains(report.Steps[0].Command, "python-apps-base/manifests/bt-x") {
		t.Errorf("expected one registry delete for the tag, got %+v", report.Steps)
	}
	if strings.Contains(report.Steps[0].Command, "bt-other") {
		t.Error("another session's tag must not be deleted")
	}
}
