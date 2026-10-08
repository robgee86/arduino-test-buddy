# Operations and their guarantees

The operations split by where they run. `up`, `down`, `status`, `push` and `prune` act on the developer machine and need no board. The others target one board (`--board`) and one session, named by the dev image tag (`--tag`). Without a tag the board runs its released stack. Each operation returns one result, printed as text or as JSON with `--format json`, and the MCP tools return the same result as structured content.

## Rules every board operation follows

- **Sessions take turns.** `run` and `cleanup` hold the board for their whole duration, queueing for up to `--wait` (default 30 minutes) while another session holds it, and report how long they waited. The turn is an `flock` on the board held by an ssh session whose input the tool owns, so it ends when the tool exits for any reason, `kill -9` included.
- **One App CLI at a time.** Every `arduino-app-cli` call runs under `flock -w 900 /tmp/arduino-test-buddy.lock`, since the CLI corrupts the board when run twice. A CLI typed in a shell on the board, or through `shell`, bypasses the lock.
- **Session names.** A local test app is stored on the board as `bt-<tag>-<name>`, or `bt-released-<name>` without a tag, so sessions never collide on or remove each other's apps. Apps named `bt-*` by hand before the tool need `cleanup --app <name>`.
- **What a turn does not cover.** `run --keep` leaves the app running after the turn ends, so the next session may start apps beside it. A session killed while it queues leaves its `ssh` waiting until the board frees up or the wait expires; it then takes the board for an instant and exits.
- **Dev variables only where they belong.** Calls that run apps carry `DOCKER_REGISTRY_BASE` and `DOCKER_PYTHON_BASE_IMAGE`; cleanup calls never do, since any call carrying them recreates the assets folder of the tag.

## push

Builds on the developer machine from the checkout it is given (`--source`, default the current directory) through the repository's own `task build:bricks` and `task build:containers PUSH=1`, on the tool's dedicated builder, which pushes straight into the registry. Nothing lands in Docker's own image store. Whether the checkout is a worktree is the caller's choice; the tool never creates one.

By default every container is built and pushed, so nothing a brick's compose files name can be missing on any board, and the build cache the push did not use is dropped at the end. `--targets` narrows the build and skips the pruning. Unchanged layers come from the cache, so a Python-only change rebuilds the wheel layer and little else.

The result lists every image the registry holds under the tag after the push, so a narrowed push also shows what an earlier full push of the tag left. Every image carries the commit it was built from in its `org.opencontainers.image.revision` label, with a `-dirty` suffix for an unclean tree. The tag defaults to the branch name made safe for an image tag; a detached HEAD uses `bt-<worktree folder>`, so parallel worktrees at the same commit never share a tag.

## up, down, status

The tool runs two containers on the developer machine: the registry the boards pull from and the builder, which buildx runs in its own container. Nothing runs until `up`, and neither container restarts with Docker or after a reboot.

- `up` creates what is missing and starts what is stopped, then waits until the registry answers.
- `down` waits for running pushes, prunes and board runs to end, then stops both. Their data stays for the next `up`.
- `status` reports both states, the registry folder and its size, the build cache size and the images, and starts nothing.

`push`, `prune --tag` and `prune --cache` need the tool up, and so does a `run` that pulls from the host registry; while it is down they fail at once and ask for `up`, before taking a turn on any board. See [disk.md](disk.md).

## prune

Gives back disk on the developer machine, after running pushes and board runs end. `--tag` deletes the tag's images and frees the layers no other tag uses; `--cache` empties the build cache; `--all` removes the registry container, its folder and the builder with its cache. It touches nothing outside the tool's registry, folder and builder.

## preflight

One round trip returning the board state as facts: hostname, CLI version, free disk, apps, app folders, containers, images, which images of the tag the board already pulled, models, assets folders, video devices and audio cards.

## examples

Lists the shipped examples of a brick, named by the brick id (`video_object_detection`) or its folder, with the `examples:<path>` name `run` takes and whether the example has a sketch, which flashes the MCU.

## run

Copies the local app folder if given, holds the host registry so a `down` waits for the run, waits for the session's turn, opens a tunnel from the board to the host registry on a fresh port, starts the app or example, polls the log and the container state, and stops the app unless asked to keep it running. The board pulls only the images the app's compose files name. The result is the evidence of the run:

- The log of this run only, cut at the last `App is starting` line, so an old traceback never shadows a healthy restart.
- Each container with its image and the revision label the image was built from.
- Whether the marker appeared, the run timed out, or the app exited first; a crash returns its traceback at once instead of after the timeout.
- On a failed start, the start output and the brick variables it asked for.
- How long the session waited for the board.

Test apps default to the `BOARD-TEST SUMMARY` marker, shipped examples to the framework's `App started` line; `--marker` takes any regex for a stronger line. An example with a sketch is refused unless `--allow-flash` is given. A first start that pulls a runner image can take minutes.

## logs, exec, shell

`logs` returns the app log cut to the last run, or whole with `--all`. `exec` runs a shell command inside a container of the board, `shell` on the board's host OS; both return stdout, stderr and the exit code. Neither is meant for `arduino-app-cli`, which must stay under the lock. None of them takes a turn.

## cleanup

Waits for the session's turn, then removes in this order and only this: the session's `bt-<tag>-*` apps and their folders through `app destroy`; the containers with their volumes and the networks of those apps and of the named brick's examples, except an example another session kept running, which is reported; the images of the tag pulled on the board, under any tunnel port; the assets folder of the tag, last.

Data written inside shipped example folders is reported, never deleted, since it sits among shipped files. The result lists every step with its output and ends with what the board still holds. `--dry-run` prints the plan without running it. The host registry is untouched: `prune` handles it once the branch is done.
