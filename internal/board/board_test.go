// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeRunner answers scripts by regex and records what ran, so tests check behavior without a board.
type fakeRunner struct {
	answers []answer
	scripts []string
	pushed  []string
	leases  []string
}

type answer struct {
	match *regexp.Regexp
	out   Output
}

// fakePort is the board-side tunnel port every fake turn gets.
const fakePort = 23456

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

func (f *fakeRunner) Push(_ context.Context, localDir, remoteDir string) error {
	f.pushed = append(f.pushed, localDir+" -> "+remoteDir)
	return nil
}

func (f *fakeRunner) Lease(_ context.Context, hostPort string, _ time.Duration) (*Lease, error) {
	f.leases = append(f.leases, hostPort)
	if hostPort == "" {
		return &Lease{}, nil
	}
	return &Lease{Port: fakePort}, nil
}

func (f *fakeRunner) ran(pattern string) bool {
	re := regexp.MustCompile(pattern)
	return slices.ContainsFunc(f.scripts, re.MatchString)
}

// poll is what the run loop reads in one round trip: the app log and the state of its containers.
func poll(log, state string) string { return "@@logs\n" + log + "@@state\n" + state + "\n" }

const startedEvent = `{"appName":"x","status":"started","output":{"stdout":"[INFO] Starting app\n","stderr":""}}` + "\n"

// restartedEvent is what app restart prints, whether the app was running or not.
const restartedEvent = `{"app_name":"x","status":"restarted","output":{"stdout":"[INFO] Starting app\n","stderr":""}}` + "\n"

const twoRunsLog = `[main] Creating virtual environment at: .cache/.venv
[main] ======== App is starting ============================
[main] BOARD-TEST FAIL: old
[main] ======== App is starting ============================
[main] BOARD-TEST PASS: probe
[main] BOARD-TEST SUMMARY: 1/1
`

func TestAppCLIIsSerializedAndCarriesDevVariablesOnlyWhenGiven(t *testing.T) {
	dev := appCLI(Dev{Registry: LoadedRegistry, Tag: "bt-llm"}, "app", "start", "bt-a")
	want := "flock -w 900 /tmp/arduino-test-buddy.lock env DOCKER_REGISTRY_BASE=dev.local/ DOCKER_PYTHON_BASE_IMAGE=app-bricks/python-apps-base:bt-llm arduino-app-cli app start bt-a"
	if dev != want {
		t.Errorf("dev call:\n got %s\nwant %s", dev, want)
	}
	if bare := appCLI(Dev{}, "app", "destroy", "bt-a"); strings.Contains(bare, "DOCKER_") {
		t.Errorf("a call without dev must not carry variables: %s", bare)
	}
}

func TestSessionAppNamesAreScopedToTheTag(t *testing.T) {
	b := New("x", &fakeRunner{}, Dev{Tag: "feature.x"})
	for in, want := range map[string]string{
		"bt-mytest":                 "bt-feature-x-mytest",
		"mytest":                    "bt-feature-x-mytest",
		"bt-feature-x-mytest":       "bt-feature-x-mytest",
		"examples:bricks/arduino/a": "examples:bricks/arduino/a",
	} {
		if got := b.SessionApp(in); got != want {
			t.Errorf("SessionApp(%q) = %q, want %q", in, got, want)
		}
	}
	if got := New("x", &fakeRunner{}, Dev{}).SessionApp("bt-mytest"); got != "bt-released-mytest" {
		t.Errorf("without a tag the app belongs to the released session: %q", got)
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

func TestRunTakesATurnAndPullsThroughItsTunnel(t *testing.T) {
	f := (&fakeRunner{}).
		on(`app restart .*bt-x-probe`, restartedEvent).
		on(`app logs .*bt-x-probe`, poll(twoRunsLog, "running")).
		on(`docker ps`, "bt-x-probe-main-1|localhost:23456/app-bricks/python-apps-base:bt-x|abc123\n")
	res, err := New("x", f, Dev{Tag: "bt-x"}).Run(context.Background(), RunRequest{App: "bt-probe"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Started || !res.MarkerFound || res.TimedOut || !res.Stopped || res.App != "bt-x-probe" {
		t.Errorf("unexpected verdict: %+v", res)
	}
	if strings.Contains(res.Log, "old") {
		t.Error("log must hold the last run only")
	}
	if len(res.Containers) != 1 || res.Containers[0].Revision != "abc123" {
		t.Errorf("containers: %+v", res.Containers)
	}
	if len(f.leases) != 1 || f.leases[0] == "" {
		t.Errorf("run must take one turn with a tunnel to the host registry: %v", f.leases)
	}
	if !f.ran(`DOCKER_REGISTRY_BASE=localhost:23456/ .* app stop /home/arduino/ArduinoApps/bt-x-probe`) {
		t.Error("the app must start and stop through the turn's tunnel port")
	}
}

type downHost struct{}

func (downHost) Use(context.Context) (func(), error) { return nil, errors.New("down") }

func TestRunFailsBeforeItsTurnWhenTheHostRegistryIsDown(t *testing.T) {
	f := &fakeRunner{}
	b := New("x", f, Dev{Tag: "bt-x"})
	b.Host = downHost{}
	if _, err := b.Run(context.Background(), RunRequest{App: "bt-probe"}); err == nil || len(f.leases) != 0 || f.ran(`app (re)?start`) {
		t.Errorf("a run must fail without taking a turn: %v %v", err, f.leases)
	}
}

func TestRunWithLoadedImagesOpensNoTunnel(t *testing.T) {
	f := (&fakeRunner{}).on(`app restart`, restartedEvent).on(`app logs`, poll(twoRunsLog, "running"))
	if _, err := New("x", f, Dev{Registry: LoadedRegistry, Tag: "bt-x"}).Run(context.Background(), RunRequest{App: "bt-probe"}); err != nil {
		t.Fatal(err)
	}
	if len(f.leases) != 1 || f.leases[0] != "" || !f.ran(`DOCKER_REGISTRY_BASE=dev.local/`) {
		t.Errorf("a turn without tunnel and the loaded prefix expected: %v", f.leases)
	}
}

func TestRunReportsMissingVariables(t *testing.T) {
	f := (&fakeRunner{}).onExit(`app restart .*bt-released-mqtt`, 1, `Error: variable "MQTT_BROKER" is required by brick arduino:mqtt`)
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

func TestRunStopsPollingWhenTheContainerExited(t *testing.T) {
	f := (&fakeRunner{}).
		on(`app restart .*bt-released-crash`, restartedEvent).
		on(`app logs .*bt-released-crash`, poll("[main] ======== App is starting ====\n[main] Traceback: boom\n", "exited"))
	res, err := New("x", f, Dev{}).Run(context.Background(), RunRequest{App: "bt-crash", TimeoutS: 600})
	if err != nil {
		t.Fatal(err)
	}
	if res.MarkerFound || res.TimedOut || !strings.Contains(res.Error, "exited") || !strings.Contains(res.Log, "boom") {
		t.Errorf("expected an early exit with the traceback: %+v", res)
	}
}

func TestRunRefusesExamplesWithSketchUnlessAllowed(t *testing.T) {
	f := (&fakeRunner{}).on(`test -d .*/sketch`, "")
	res, err := New("x", f, Dev{}).Run(context.Background(), RunRequest{App: "examples:bricks/arduino/motion_detection/01_basic_usage"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Started || !strings.Contains(res.Error, "flash") || f.ran(`app (re)?start`) {
		t.Errorf("expected a refusal without a start, got %+v", res)
	}
}

func TestExamplesDefaultToTheStartupMarker(t *testing.T) {
	f := (&fakeRunner{}).
		onExit(`test -d .*/sketch`, 1, "").
		on(`app start examples:`, startedEvent).
		on(`app logs examples:`, poll("[main] ======== App is starting ====\n[main] 2026 INFO - App:  App started\n", "running"))
	res, err := New("x", f, Dev{}).Run(context.Background(), RunRequest{App: "examples:bricks/arduino/weather_forecast/01_city"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Marker != ExampleMarker || !res.MarkerFound {
		t.Errorf("expected the startup marker to end the run: %+v", res)
	}
}

func TestRunPushesLocalDirUnderTheSessionName(t *testing.T) {
	dir := t.TempDir() + "/bt-local"
	if err := mkAppDir(dir); err != nil {
		t.Fatal(err)
	}
	f := (&fakeRunner{}).on(`app restart .*bt-x-local`, restartedEvent).on(`app logs`, poll(twoRunsLog, "running"))
	res, err := New("x", f, Dev{Tag: "x"}).Run(context.Background(), RunRequest{LocalDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.App != "bt-x-local" || len(f.pushed) != 1 || !strings.HasSuffix(f.pushed[0], " -> "+AppsDir+"/bt-x-local") {
		t.Errorf("push: %v app: %s", f.pushed, res.App)
	}
}

func TestDecodeAppID(t *testing.T) {
	if got := DecodeAppID("ZXhhbXBsZXM6aW5zcGlyYXRpb25hbC9lZGdlLWFpLWFzc2lzdGFudA"); got != "examples:inspirational/edge-ai-assistant" {
		t.Errorf("got %q", got)
	}
}

func TestExamplesMatchTheBrickIdOrFolder(t *testing.T) {
	list := `{"apps":[
	 {"id":"ZXhhbXBsZXM6YnJpY2tzL2FyZHVpbm8vbW90aW9uX2RldGVjdGlvbi8wMV9iYXNpY191c2FnZQ","name":"Motion"},
	 {"id":"ZXhhbXBsZXM6aW5zcGlyYXRpb25hbC9lZGdlLWFpLWFzc2lzdGFudA","name":"Edge AI"},
	 {"id":"dXNlcjpidC1wcm9iZQ","name":"bt-probe"}]}`
	sketch := "@@sketch\n" + ExamplesDir + "/bricks/arduino/motion_detection/01_basic_usage/sketch\n"
	for name, brickSection := range map[string]string{
		"declared in app.yaml": "@@brick\n" + ExamplesDir + "/bricks/arduino/motion_detection/01_basic_usage/app.yaml\n",
		"by folder name":       "@@brick\n",
	} {
		f := (&fakeRunner{}).on(`app list --examples`, "@@list\n"+list+"\n"+sketch+brickSection)
		examples, err := New("x", f, Dev{}).Examples(context.Background(), "motion_detection")
		if err != nil {
			t.Fatal(err)
		}
		if len(examples) != 1 || examples[0].App != "examples:bricks/arduino/motion_detection/01_basic_usage" || !examples[0].HasSketch {
			t.Errorf("%s: %+v", name, examples)
		}
	}
}

func TestCleanupTakesATurnAndTouchesOnlyTheSession(t *testing.T) {
	inventory := "@@apps\nbt-mqtt-probe\nbt-other-probe\ntest_cam\n" +
		"@@containers\nbt-mqtt-probe-main-1|exited\nbt-other-probe-main-1|running\ntest_cam-main-1|exited\n" +
		"var-lib-arduino-app-cli-examples-bricks-arduino-mqtt-01_basic-main-1|exited\n" +
		"var-lib-arduino-app-cli-examples-bricks-arduino-mqtt-02_kept-main-1|running\n" +
		"var-lib-arduino-app-cli-examples-bricks-arduino-llm-01-main-1|exited\n" +
		"@@networks\nbt-mqtt-probe_default\nbt-other-probe_default\nbridge\nvar-lib-arduino-app-cli-examples-bricks-arduino-mqtt-01_basic_default\n" +
		"@@images\nlocalhost:23456/app-bricks/python-apps-base:bt-mqtt\nlocalhost:21111/app-bricks/ei-models-runner:bt-mqtt\n" +
		"localhost:23456/app-bricks/python-apps-base:bt-other\nghcr.io/arduino/app-bricks/python-apps-base:0.13.1\n" +
		"@@assets\n0.13.1\nbt-mqtt\nbt-other\n" +
		"@@leftovers\n" + ExamplesDir + "/bricks/arduino/mqtt/01_basic/data\n"
	f := (&fakeRunner{}).on(`@@apps`, inventory)
	report, err := New("x", f, Dev{Tag: "bt-mqtt"}).Cleanup(context.Background(), CleanupRequest{Brick: "mqtt", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, s := range report.Steps {
		commands = append(commands, s.Command)
	}
	joined := strings.Join(commands, "\n")
	for _, want := range []string{
		"arduino-app-cli app destroy /home/arduino/ArduinoApps/bt-mqtt-probe",
		"rm -rf /home/arduino/ArduinoApps/bt-mqtt-probe",
		"docker rm -f -v bt-mqtt-probe-main-1 var-lib-arduino-app-cli-examples-bricks-arduino-mqtt-01_basic-main-1",
		"docker network rm bt-mqtt-probe_default var-lib-arduino-app-cli-examples-bricks-arduino-mqtt-01_basic_default",
		"docker rmi localhost:23456/app-bricks/python-apps-base:bt-mqtt localhost:21111/app-bricks/ei-models-runner:bt-mqtt",
		"rm -rf /var/lib/arduino-app-cli/assets/bt-mqtt",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing step %q in:\n%s", want, joined)
		}
	}
	for _, forbidden := range []string{"bt-other", "test_cam", "02_kept", "arduino-llm", "0.13.1", "bridge", "DOCKER_"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("cleanup must not touch %q:\n%s", forbidden, joined)
		}
	}
	if !strings.HasSuffix(commands[len(commands)-1], "assets/bt-mqtt") {
		t.Error("the assets folder must go last")
	}
	if !slices.ContainsFunc(report.Warnings, func(w string) bool { return strings.Contains(w, "02_kept") }) {
		t.Errorf("an example kept running by another session must be reported: %v", report.Warnings)
	}
	if len(f.leases) != 1 || f.leases[0] != "" {
		t.Errorf("cleanup must take a turn without a tunnel: %v", f.leases)
	}
	if f.ran(`docker rm|docker rmi|destroy|rm -rf`) {
		t.Error("dry run must not remove anything")
	}
}

func TestPreflightParsesSections(t *testing.T) {
	out := "@@hostname\nventunoq\n@@cli\nArduino App CLI version 0.14.0\ndaemon version: 0.14.0\n@@disk\n/dev/x 100 50 20480 60% /\n" +
		"@@apps\n" + `{"apps":[{"id":"dXNlcjp0ZXN0X2NhbQ","name":"Test camera","status":"stopped"}]}` + "\n@@appdirs\ntest_cam\n" +
		"@@containers\nc1|img|Up\n@@images\nlocalhost:24000/app-bricks/python-apps-base:bt-x|1GB\nghcr.io/arduino/app-bricks/python-apps-base:0.13.1|1GB\n" +
		"@@models\nm1\n@@assets\n0.13.1\n@@video\n/dev/video0\n@@audio\n 0 [UAC2 ]: USB-Audio\n"
	f := (&fakeRunner{}).on(`@@hostname`, out)
	p, err := New("ventunoq", f, Dev{Tag: "bt-x"}).Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.Hostname != "ventunoq" || p.CLIVersion != "Arduino App CLI version 0.14.0" || p.DiskFreeMB != 20 {
		t.Errorf("header: %+v", p)
	}
	if len(p.Apps) != 1 || p.Apps[0].Folder != "test_cam" {
		t.Errorf("an app is named by its folder, not its display name: %+v", p.Apps)
	}
	if len(p.Apps) != 1 || len(p.Images) != 2 || len(p.DevImages) != 1 || len(p.VideoDevices) != 1 || len(p.AudioCards) != 1 {
		t.Errorf("lists: %+v", p)
	}
}
