package board

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestCleanupLeavesTheAssetsTheReleaseShips(t *testing.T) {
	// A session tagged like the release's runner version owns no assets folder: the package does
	inventory := "@@apps\n@@containers\n@@networks\nbridge\n@@images\nghcr.io/arduino/app-bricks/python-apps-base:0.13.1\n" +
		"@@assets\n0.13.1\n@@packaged\n0.13.1\n"
	f := (&fakeRunner{}).on(`@@apps`, inventory)
	report, err := New("x", f, Dev{Tag: "0.13.1"}).Cleanup(context.Background(), CleanupRequest{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range report.Steps {
		if strings.Contains(s.Command, "assets/0.13.1") {
			t.Errorf("the assets folder of the installed release must stay: %q", s.Command)
		}
	}
}

// pulledInventory is a board where the session's apps pulled two images, one already gone, next to an image that was there before.
const pulledInventory = "@@apps\n@@containers\n@@networks\nbridge\n" +
	"@@images\ninfluxdb:2.7-alpine\ninfluxdb:2.9-alpine\n@@assets\n@@pulled\ninfluxdb:2.9-alpine\nredis:8\ninfluxdb:2.9-alpine\n@@packaged\n"

func TestCleanupRemovesTheImagesTheSessionsAppsPulledAndThenTheirRecord(t *testing.T) {
	for session, dev := range map[string]Dev{"bt-x": {Tag: "bt-x"}, "bt-released": {}} {
		f := (&fakeRunner{}).on(`@@apps`, pulledInventory)
		report, err := New("x", f, dev).Cleanup(context.Background(), CleanupRequest{DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		want := "docker rmi influxdb:2.9-alpine && { rm -f /home/arduino/.cache/arduino-test-buddy/" + session + ".pulled; rmdir /home/arduino/.cache/arduino-test-buddy 2>/dev/null || true; }"
		if len(report.Steps) != 1 || report.Steps[0].Command != want {
			t.Errorf("%s: only the image the session's apps pulled and the board still holds may go, with its record after it:\n got %+v\nwant %s", session, report.Steps, want)
		}
		if !slices.Equal(report.Remaining.PulledImages, []string{"influxdb:2.9-alpine"}) {
			t.Errorf("%s: a dry run lists what the session's apps pulled: %v", session, report.Remaining.PulledImages)
		}
	}
}

func TestCleanupForgetsARecordWhoseImagesAreGone(t *testing.T) {
	inventory := "@@apps\n@@containers\n@@networks\n@@images\ninfluxdb:2.7-alpine\n@@assets\n@@pulled\ninfluxdb:2.9-alpine\n@@packaged\n"
	f := (&fakeRunner{}).on(`@@apps`, inventory)
	report, err := New("x", f, Dev{Tag: "bt-x"}).Cleanup(context.Background(), CleanupRequest{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Steps) != 1 || strings.Contains(report.Steps[0].Command, "docker rmi") || !strings.HasPrefix(report.Steps[0].Command, "rm -f /home/arduino/.cache/arduino-test-buddy/bt-x.pulled") {
		t.Errorf("with no recorded image left only the record goes: %+v", report.Steps)
	}
}

func TestCleanupReportsAPulledImageAContainerOutsideTheSessionStillUses(t *testing.T) {
	// The board refuses to remove an image in use, so it is still there after the steps ran
	f := (&fakeRunner{}).on(`@@apps`, pulledInventory).onExit(`docker rmi`, 1, "conflict: image is being used by stopped container")
	report, err := New("x", f, Dev{Tag: "bt-x"}).Cleanup(context.Background(), CleanupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(report.Warnings, func(w string) bool { return strings.Contains(w, "influxdb:2.9-alpine") }) ||
		!slices.Equal(report.Remaining.PulledImages, []string{"influxdb:2.9-alpine"}) {
		t.Errorf("an image that could not go must be reported and stay listed: %v %v", report.Warnings, report.Remaining.PulledImages)
	}
}
