# Operations and their guarantees

Every operation targets one board (`--board`) and one session, identified by the dev image tag (`--tag`) and the registry prefix (`--registry`, default the board registry `localhost:5000/`). Without a tag the board runs its released stack. Each operation returns one result, printed as text or as JSON with `--format json`, and the MCP tools return the same result as structured content.

## Rules every operation follows

- Every `arduino-app-cli` call the tool makes runs under `flock -w 900 /tmp/arduino-test-buddy.lock` on the board, so two tool sessions never run the CLI concurrently. The CLI corrupts the board when run twice. A CLI typed in a shell on the board, or through `shell`, bypasses the lock.
- Calls that run apps carry `DOCKER_REGISTRY_BASE` and `DOCKER_PYTHON_BASE_IMAGE`; cleanup calls never do, since any call carrying them recreates the assets folder of the tag.
- Session artifacts are `bt-`-named or carry the session tag. Nothing else is ever removed.

## push

Builds on the developer machine from the checkout it is given (`--source`, default the current directory) through the repository's own `task build:bricks` and `task build:containers`, then pushes to the board registry through an SSH tunnel on a free local port. Whether the checkout is a worktree is the caller's choice; the tool never creates one.

By default every container of the bake file is built and pushed, so nothing a brick's compose files name can be missing on the board. `--targets` narrows the build. Docker sends only the layers the registry lacks: the first push of a machine moves everything, afterwards a Python-only rebuild moves the wheel layer and every unchanged image costs a manifest check.

Every image carries the commit it was built from in its `org.opencontainers.image.revision` label, with a `-dirty` suffix for an unclean tree. The tag defaults to the branch name made safe for an image tag, or `bt-<short sha>` on a detached HEAD. After the push the host copies, named after the tunnel port, are removed.

## registry

Starts the `arduino-test-buddy-registry` container on the board once, bound to loopback with a named volume, a restart policy and manifest deletion enabled, then only reports what it holds. It runs permanently on the board after the first use.

## preflight

One round trip returning the board state as facts: hostname, CLI version, free disk, apps, app folders, containers, images, which dev images are pulled and which are in the registry for the tag, models, assets folders, video devices and audio cards.

## examples

Lists the shipped examples of a brick, named by the brick id (`video_object_detection`) or its folder, with the `examples:<path>` name `run` takes and whether the example has a sketch, which flashes the MCU.

## run

Copies the local app folder if given, starts the app or example, polls the log and the container state, and stops the app unless asked to keep it running. The result is the evidence of the run:

- The log of this run only, cut at the last `App is starting` line, so an old traceback never shadows a healthy restart.
- Each container with its image and the revision label the image was built from.
- Whether the marker appeared, the run timed out, or the app exited first; a crash returns its traceback at once instead of after the timeout.
- On a failed start, the start output and the brick variables it asked for.

Test apps default to the `BOARD-TEST SUMMARY` marker, shipped examples to the framework's `App started` line; `--marker` takes any regex for a stronger line. An example with a sketch is refused unless `--allow-flash` is given. The call blocks until the marker or the timeout, and a first start that pulls a runner image can take minutes.

## logs, exec, shell

`logs` returns the app log cut to the last run, or whole with `--all`. `exec` runs a shell command inside a container of the board, `shell` on the board's host OS; both return stdout, stderr and the exit code. Neither is meant for `arduino-app-cli`, which must stay under the lock.

## cleanup

Removes, in this order and only this: `bt-*` apps and their folders through `app destroy`; the containers with their volumes and the networks of those apps and of the named brick's examples; the images of the session's registry and tag pulled on the board; the tag in the board registry, by tag so a manifest shared with another tag keeps that one; the assets folder of the tag, last. Registry blobs stay until a garbage collection, which is not part of the tool yet.

Data written inside shipped example folders is reported, never deleted, since it sits among shipped files. The result lists every step with its output and ends with what the board still holds. `--dry-run` prints the plan without running it.
