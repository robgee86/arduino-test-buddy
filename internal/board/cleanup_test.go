package board

import (
	"context"
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
