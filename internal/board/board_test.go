// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fakeRunner answers scripts by regex and records what ran, so tests check behavior without a board.
type fakeRunner struct {
	answers []answer
	scripts []string
	pushed  []string
}

type answer struct {
	match *regexp.Regexp
	out   Output
}

func (f *fakeRunner) on(pattern string, stdout string) *fakeRunner {
	f.answers = append(f.answers, answer{regexp.MustCompile(pattern), Output{Stdout: stdout}})
	return f
}

func (f *fakeRunner) onExit(pattern string, code int, stderr string) *fakeRunner {
	f.answers = append(f.answers, answer{regexp.MustCompile(pattern), Output{Stderr: stderr, ExitCode: code}})
	return f
}

func (f *fakeRunner) Run(_ context.Context, script string) (Output, error) {
	f.scripts = append(f.scripts, script)
	for _, a := range f.answers {
		if a.match.MatchString(script) {
			return a.out, nil
		}
	}
	return Output{}, nil
}

func (f *fakeRunner) Push(_ context.Context, localDir, remoteParent string) error {
	f.pushed = append(f.pushed, localDir+" -> "+remoteParent)
	return nil
}

func (f *fakeRunner) ran(pattern string) bool {
	re := regexp.MustCompile(pattern)
	for _, s := range f.scripts {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

const startedEvent = `{"appName":"bt-probe","status":"started","output":{"stdout":"[INFO] Starting app \"bt-probe\"\n","stderr":""}}` + "\n"

// poll is what the run loop reads in one round trip: the app log and the state of its containers.
func poll(log, state string) string { return "@@logs\n" + log + "@@state\n" + state + "\n" }

const twoRunsLog = `[main] Creating virtual environment at: .cache/.venv
[main] ======== App is starting ============================
[main] BOARD-TEST FAIL: old
[main] ======== App is starting ============================
[main] BOARD-TEST PASS: probe
[main] BOARD-TEST SUMMARY: 1/1
`

func TestAppCLIIsSerializedAndCarriesDevVariablesOnlyWhenAsked(t *testing.T) {
	b := New("x", &fakeRunner{}, Dev{Registry: LoadedRegistry, Tag: "bt-llm"})
	dev := b.appCLI(true, "app", "start", "bt-a")
	want := "flock -w 900 /tmp/arduino-board-tool.lock env DOCKER_REGISTRY_BASE=dev.local/ DOCKER_PYTHON_BASE_IMAGE=app-bricks/python-apps-base:bt-llm arduino-app-cli app start bt-a"
	if dev != want {
		t.Errorf("dev call:\n got %s\nwant %s", dev, want)
	}
	bare := b.appCLI(false, "app", "destroy", "bt-a")
	if strings.Contains(bare, "DOCKER_") {
		t.Errorf("bare call must not carry dev variables: %s", bare)
	}
	released := New("x", &fakeRunner{}, Dev{}).appCLI(true, "app", "start", "a")
	if strings.Contains(released, "env") {
		t.Errorf("released stack must not set variables: %s", released)
	}
}

func TestLastRun(t *testing.T) {
	got := LastRun(twoRunsLog)
	if strings.Contains(got, "old") || !strings.HasPrefix(got, "[main] ======== App is starting") {
		t.Errorf("LastRun kept the earlier run:\n%s", got)
	}
	if LastRun("no boundary\n") != "no boundary\n" {
		t.Error("a log without a boundary must come back whole")
	}
}

func TestRunWaitsForMarkerInLastRunThenStops(t *testing.T) {
	f := (&fakeRunner{}).
		on(`app start .*bt-probe`, startedEvent).
		on(`app logs .*bt-probe`, poll(twoRunsLog, "running")).
		on(`docker ps`, "bt-probe-main-1|dev.local/app-bricks/python-apps-base:bt-x|abc123\n")
	b := New("x", f, Dev{Tag: "bt-x"})
	res, err := b.Run(context.Background(), RunRequest{App: "bt-probe"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Started || !res.MarkerFound || res.TimedOut || !res.Stopped {
		t.Errorf("unexpected verdict: %+v", res)
	}
	if strings.Contains(res.Log, "old") {
		t.Error("log must hold the last run only")
	}
	if len(res.Containers) != 1 || res.Containers[0].Revision != "abc123" {
		t.Errorf("containers: %+v", res.Containers)
	}
	if !f.ran(`DOCKER_PYTHON_BASE_IMAGE=app-bricks/python-apps-base:bt-x arduino-app-cli app stop /home/arduino/ArduinoApps/bt-probe`) {
		t.Error("stop must run with the session variables")
	}
}

func TestRunReportsMissingVariables(t *testing.T) {
	f := (&fakeRunner{}).onExit(`app start .*bt-mqtt`, 1, `Error: variable "MQTT_BROKER" is required by brick arduino:mqtt`)
	res, err := New("x", f, Dev{}).Run(context.Background(), RunRequest{App: "bt-mqtt"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Started || len(res.MissingVariables) != 1 || res.MissingVariables[0] != "MQTT_BROKER" {
		t.Errorf("unexpected result: %+v", res)
	}
	if f.ran(`app logs`) || f.ran(`app stop`) {
		t.Error("a failed start must not poll or stop")
	}
}

func TestRunRefusesExamplesWithSketchUnlessAllowed(t *testing.T) {
	f := (&fakeRunner{}).on(`test -d .*/sketch`, "")
	res, err := New("x", f, Dev{}).Run(context.Background(), RunRequest{App: "examples:bricks/arduino/motion_detection/01_basic_usage"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Started || !strings.Contains(res.Error, "flash") {
		t.Errorf("expected a refusal, got %+v", res)
	}
	if f.ran(`app start`) {
		t.Error("must not start")
	}
}

func TestRunPushesLocalDirAndNamesTheApp(t *testing.T) {
	dir := t.TempDir() + "/bt-local"
	if err := mkAppDir(dir); err != nil {
		t.Fatal(err)
	}
	f := (&fakeRunner{}).on(`app start .*bt-local`, startedEvent).on(`app logs`, poll(twoRunsLog, "running"))
	res, err := New("x", f, Dev{}).Run(context.Background(), RunRequest{LocalDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.App != "bt-local" || len(f.pushed) != 1 || !strings.HasSuffix(f.pushed[0], " -> "+AppsDir) {
		t.Errorf("push: %v app: %s", f.pushed, res.App)
	}
}

func TestDecodeAppID(t *testing.T) {
	if got := DecodeAppID("ZXhhbXBsZXM6aW5zcGlyYXRpb25hbC9lZGdlLWFpLWFzc2lzdGFudA"); got != "examples:inspirational/edge-ai-assistant" {
		t.Errorf("got %q", got)
	}
	if got := DecodeAppID("dXNlcjpidC1wcm9iZQ"); got != "user:bt-probe" {
		t.Errorf("got %q", got)
	}
}

func TestExamplesFiltersByDeclaredBrickAndFlagsSketches(t *testing.T) {
	list := `{"apps":[
	 {"id":"ZXhhbXBsZXM6YnJpY2tzL2FyZHVpbm8vbW90aW9uX2RldGVjdGlvbi8wMV9iYXNpY191c2FnZQ","name":"Motion"},
	 {"id":"ZXhhbXBsZXM6aW5zcGlyYXRpb25hbC9lZGdlLWFpLWFzc2lzdGFudA","name":"Edge AI"},
	 {"id":"dXNlcjpidC1wcm9iZQ","name":"bt-probe"}]}`
	f := (&fakeRunner{}).on(`app list --examples`, "@@list\n"+list+"\n@@sketch\n"+ExamplesDir+"/bricks/arduino/motion_detection/01_basic_usage/sketch\n@@brick\n"+ExamplesDir+"/bricks/arduino/motion_detection/01_basic_usage/app.yaml\n")
	examples, err := New("x", f, Dev{}).Examples(context.Background(), "motion_detection")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 1 || examples[0].App != "examples:bricks/arduino/motion_detection/01_basic_usage" || !examples[0].HasSketch {
		t.Errorf("examples: %+v", examples)
	}
}

func TestCleanupStepsAreScopedToTheSession(t *testing.T) {
	inventory := "@@apps\n" + `{"apps":[{"name":"bt-probe"},{"name":"test_cam"}]}` + "\n" +
		"@@containers\nbt-probe-main-1\ntest_cam-main-1\nvar-lib-arduino-app-cli-examples-bricks-arduino-mqtt-01_basic-main-1\nvar-lib-arduino-app-cli-examples-bricks-arduino-llm-01-main-1\n" +
		"@@networks\nbt-probe_default\nbridge\nvar-lib-arduino-app-cli-examples-bricks-arduino-mqtt-01_basic_default\n" +
		"@@images\ndev.local/app-bricks/python-apps-base:bt-mqtt\ndev.local/app-bricks/python-apps-base:bt-other\nghcr.io/arduino/app-bricks/python-apps-base:0.13.1\n" +
		"@@assets\n0.13.1\nbt-mqtt\nbt-other\n" +
		"@@leftovers\n" + ExamplesDir + "/bricks/arduino/mqtt/01_basic/data\n"
	f := (&fakeRunner{}).on(`@@apps`, inventory)
	b := New("x", f, Dev{Registry: LoadedRegistry, Tag: "bt-mqtt"})
	report, err := b.Cleanup(context.Background(), CleanupRequest{Brick: "mqtt", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, s := range report.Steps {
		commands = append(commands, s.Command)
	}
	joined := strings.Join(commands, "\n")
	for _, want := range []string{
		"flock -w 900 /tmp/arduino-board-tool.lock arduino-app-cli app destroy /home/arduino/ArduinoApps/bt-probe",
		"rm -rf /home/arduino/ArduinoApps/bt-probe",
		"docker rm -f -v bt-probe-main-1 var-lib-arduino-app-cli-examples-bricks-arduino-mqtt-01_basic-main-1",
		"docker network rm bt-probe_default var-lib-arduino-app-cli-examples-bricks-arduino-mqtt-01_basic_default",
		"docker rmi dev.local/app-bricks/python-apps-base:bt-mqtt",
		"rm -rf /var/lib/arduino-app-cli/assets/bt-mqtt",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing step %q in:\n%s", want, joined)
		}
	}
	for _, forbidden := range []string{"test_cam", "arduino-llm", "bt-other", "0.13.1", "bridge", "DOCKER_"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("cleanup must not touch %q:\n%s", forbidden, joined)
		}
	}
	if !strings.HasSuffix(commands[len(commands)-1], "assets/bt-mqtt") {
		t.Error("the assets folder must go last")
	}
	if len(report.Remaining.ExampleLeftovers) != 1 || len(report.Warnings) == 0 {
		t.Errorf("leftovers must be reported, not deleted: %+v", report)
	}
	if f.ran(`docker rm|docker rmi|destroy|rm -rf`) {
		t.Error("dry run must not remove anything")
	}
	if slices.Contains(report.Remaining.Networks, "bridge") {
		t.Error("docker's own networks must not be listed as remaining")
	}
}

func TestPreflightParsesSections(t *testing.T) {
	out := "@@hostname\nventunoq\n@@cli\nArduino App CLI version 0.14.0\ndaemon version: 0.14.0\n@@disk\n/dev/x 100 50 20480 60% /\n" +
		"@@apps\n" + `{"apps":[{"name":"test_cam","status":"stopped"}]}` + "\n@@appdirs\ntest_cam\n" +
		"@@containers\nc1|img|Up\n@@images\ndev.local/app-bricks/python-apps-base:bt-x|1GB\nghcr.io/arduino/app-bricks/python-apps-base:0.13.1|1GB\n" +
		"@@models\nm1\n@@assets\n0.13.1\n@@video\n/dev/video0\n@@audio\n 0 [UAC2 ]: USB-Audio\n"
	f := (&fakeRunner{}).on(`@@hostname`, out)
	p, err := New("ventunoq", f, Dev{Registry: LoadedRegistry, Tag: "bt-x"}).Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.Hostname != "ventunoq" || p.CLIVersion != "Arduino App CLI version 0.14.0" || p.DiskFreeMB != 20 {
		t.Errorf("header: %+v", p)
	}
	if len(p.Apps) != 1 || len(p.Images) != 2 || len(p.DevImages) != 1 || len(p.VideoDevices) != 1 || len(p.AudioCards) != 1 {
		t.Errorf("lists: %+v", p)
	}
}
