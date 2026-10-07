# arduino-board-tool

Run and clean up App Bricks tests on a real board from the developer's machine. One binary, two entry points: a CLI for humans and CI, and an MCP server over stdio for Claude Code and other agents. Both call the same operations and return the same structured results.

The tool exists because board testing by hand costs many SSH round trips, shell quoting mistakes and a few recurring errors: running two `arduino-app-cli` instances at once, reading an old traceback above a healthy restart, deleting another session's images at cleanup. Every operation here is scoped to one session and returns facts, not prose.

## Install

Requires Go 1.26 and SSH access to the board through an alias in `~/.ssh/config`.

```console
$ go install github.com/arduino/arduino-board-tool/cmd/arduino-board-tool@latest
```

Register it once as a user-scoped MCP server so every project can use it:

```console
$ claude mcp add --scope user arduino-board -- arduino-board-tool mcp
```

## Usage

Every command takes `--board <ssh alias>` (or `ARDUINO_BOARD`) and, for sessions on development images, `--tag <dev tag>` (or `ARDUINO_BOARD_TAG`). Without a tag the board runs the released stack. `--format json` prints the full result.

A session on a branch goes: push the images from a checkout, run the test apps and the examples, clean up by tag.

```console
$ cd ~/Code/app-bricks-py                       # or a worktree, if the prompt wants one per agent
$ arduino-board-tool --board ventunoq push       # tag defaults to the branch name; prints the tag to use below
$ arduino-board-tool --board ventunoq --tag object-tracking preflight
$ arduino-board-tool --board ventunoq --tag object-tracking run --dir ./bt-mytest
$ arduino-board-tool --board ventunoq --tag object-tracking examples --brick weather_forecast
$ arduino-board-tool --board ventunoq --tag object-tracking run examples:bricks/arduino/weather_forecast/01_weather_forecast_by_city_example
$ arduino-board-tool --board ventunoq --tag object-tracking run examples:bricks/arduino/mqtt/01_basic_usage --marker 'Connected'
$ arduino-board-tool --board ventunoq logs bt-mytest
$ arduino-board-tool --board ventunoq exec bt-mytest-main-1 'cat /proc/net/tcp'
$ arduino-board-tool --board ventunoq --tag object-tracking cleanup --brick weather_forecast --dry-run
```

Images loaded by hand with `docker save | ssh docker load` under `dev.local/` still work: pass `--registry dev.local/`.

The MCP tools are `board_preflight`, `board_registry`, `board_push`, `board_examples`, `board_run`, `board_logs`, `board_exec`, `board_shell` and `board_cleanup`, with the same parameters as the commands plus optional `board`, `tag` and `registry` fields.

## What each operation guarantees

- `push` builds on the developer machine from the checkout it is given through its own `task build:bricks` and `task build:containers`, never creating worktrees itself, and pushes through an SSH tunnel to a registry container on the board bound to loopback. By default it builds and pushes every container, so nothing a brick's compose files name can be missing; `--targets` narrows it. Docker sends only the layers the registry lacks: the first push of a machine moves everything, afterwards a Python-only rebuild moves the wheel layer and the rest costs a manifest check each. Every image carries the commit it was built from in its `org.opencontainers.image.revision` label, with a `-dirty` suffix for an unclean tree, and `run` shows that label per container.
- `registry` is idempotent: it starts the `arduino-board-tool-registry` container with a restart policy and a named volume once, then only reports.

- Every `arduino-app-cli` call the tool makes runs under `flock` on the board, so two tool sessions never run the CLI concurrently. A CLI typed in a shell on the board, or through `shell`, bypasses the lock.
- Calls that run apps carry the dev-image variables; cleanup calls never do, since any call carrying them recreates the assets folder of the tag.
- `run` waits for a marker regex in the log of this run only, cut at the last `App is starting` line, and reports each container with the revision label its image was built from. Test apps default to `BOARD-TEST SUMMARY`, shipped examples to the framework's `App started` line; pass `--marker` for a stronger line the example prints. Polling stops early when the app's container exits, so a crash returns its traceback at once. An example with a sketch is refused unless `--allow-flash` is given, because it flashes the MCU. A failed start returns the brick variables it asked for.
- `run` blocks until the marker or the timeout. A first start that pulls a runner image can take minutes; raise the MCP tool timeout of your client for such runs.
- `cleanup` removes only `bt-*` apps and their folders, the containers with their volumes and the networks of those apps and of the named brick's examples, the images of the session's registry and tag pulled on the board, the tag in the board registry (by tag, so a manifest shared with another tag keeps that one), and the assets folder of the tag, last. Registry blobs stay until a garbage collection, which is not part of this tool yet. Data written inside shipped example folders is reported, never deleted. The result ends with what the board still holds.

## Development

```console
$ task check            # gofmt, vet, tests
$ task build            # dist/arduino-board-tool
$ task install          # into GOPATH/bin
$ task release:snapshot # every platform, needs goreleaser
```

Tests use a fake runner and never touch a board.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
