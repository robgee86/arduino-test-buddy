---
name: board-test
description: Run functional tests for the App Bricks on a real target board over SSH. Use when explicitly asked to run tests or a test task on a board, or when a behavior can only be confirmed on real hardware (cameras, microphones, NPU, MCU, real containers runtime).
---

# Functional tests on a target board

Functionally test the bricks (`src/`), their runtime and their utils of an `app-bricks-py` checkout on a real board, through `arduino-test-buddy`: its MCP tools (`buddy_*` on the developer machine, `board_*` on a board) or its CLI, which runs the same operations. Never drive `arduino-app-cli` over raw SSH: the tool serializes its calls, cuts logs to the current run and scopes cleanup to the session, and raw calls bypass all three.

One contract: **every artifact of the session is `bt-`-named or otherwise unambiguously session-owned, the verdict rests on explicit evidence, and everything is removable at cleanup**.

## Hard rules

- **Single arduino-app-cli instance.** Two concurrent instances can corrupt the board. The tool locks its own calls; do not start the CLI any other way while a session runs.
- **Cleanup only after all planned tests are green.** On a failure leave the board as it is, report, and ask before cleaning.
- **Watch disk usage.** Compare the preflight's free space with the board descriptor's `storage.min_free_for_testing` plus what the session will push or pull. If short, stop and ask.
- **Boards are shared and stateful.** No system-level changes without asking: packages, services, boot config, `~/.profile`, udev rules. Disposable `bt-*` data under CLI-owned paths is fine and is removed at cleanup.
- **Examples with a sketch flash the MCU.** The tool refuses them unless told otherwise; ask the user first.

## Workflow

1. **Select the board.** Match the test's hardware and feature needs against `boards/*.yaml` in this skill's folder; `~/.config/arduino-test-buddy/boards.yaml` (per developer, never shipped: copy [boards.example.yaml](boards.example.yaml) there and edit) gives the SSH alias, the RAM variant of the unit (`ram`, the model's default when omitted) and the peripherals the developer can plug. Ask when none or several fit.
2. **Preflight.** `board_preflight` with the session tag: free disk, CLI version, kernel version, apps, containers, images and the session's pulled images, models, assets, video devices and audio cards. Find the devices each planned test opens: `required_devices` in `brick_config.yaml`, or a `Base*` peripheral parameter defaulting to `None`, which opens index 0. With several devices of a kind, list them: index 0 may not be the expected one. Ask before starting if a device or feature is missing.
3. **Push the images.** The tool runs nothing in the background: power it up with `buddy_up` when a call reports it down, and power it down with `buddy_down` only when the user asks, since other sessions may be using it. `buddy_push` builds the wheel and every container image from the current checkout and pushes them to the registry on this machine; boards pull only what their apps use, through a tunnel that lives for one run, so one push serves several boards. The checkout is whatever the prompt set up: one shared tree for a branch, one worktree per agent when agents run in parallel; a detached worktree gets its own tag from its folder name. The tag is the whole session key: sessions sharing a tag on one board share its apps, and either one's run restarts them and cleanup removes them, so parallel agents need distinct tags. The first push of a machine builds everything, minutes and several GB of build cache, so `buddy_push` from MCP needs a raised tool timeout for it. Images with a fixed registry (the Qualcomm services, InfluxDB) are pulled as they are on the first start. The tag defaults to the branch name; pass it to every later call. A tag lives 3 days from its last push: `buddy_up`, `buddy_down` and a full push drop older ones, and a board run does not count as a push, so push again when a start cannot pull the image.
4. **Write the test app** from [references/app-template.py](references/app-template.py) in a local `bt-<name>/` folder with the structure of an app (`app.yaml`, `python/main.py`). Cover every public path the change touched, error paths included. Lint with `uv run ruff check`. Hardware you cannot drive: a fake subclassing the peripheral's `Base*` class, and the report says the real device was not covered.
5. **Run.** `board_run` on `host:<folder>` copies the folder as `bt-<tag>-<name>`, waits for the session's turn on the board, starts it, waits for `BOARD-TEST SUMMARY`, returns the log of this run and each container with the commit its image was built from and the digest of its dev image, listing apart the dev images whose digest differs from what the host registry serves, then stops it. Sessions on one board take turns: the result says how long this one waited. A failed start returns the brick variables it asked for. Set them under the tool's lock, `board_shell` with `flock -w 900 /tmp/arduino-test-buddy.lock arduino-app-cli app brick config <app> <brick> NAME=value`, never with real secrets, those are the user's. On an example this edits the shipped `app.yaml` in place and cleanup does not revert it: say so in the report.
6. **Run the examples.** `board_examples` lists the shipped examples declaring the brick; read each `main.py` in `app-bricks-examples` first, an example can declare a brick without calling it. `board_run` on `examples:<path>` defaults the marker to the framework's `App started` line; pass a regex the example prints for stronger evidence, and judge from the returned log.
7. **Verify.** A test passes on explicit evidence: markers, expected log lines, expected outputs, the revision column equal to the branch HEAD and no differing images; a differing image means the board ran a stale or swapped one, so push again and rerun, or ask. "The app started" is not evidence. For other test forms see [references/test-forms.md](references/test-forms.md).
8. **Cleanup** when all green: `board_cleanup` with the tag and the brick waits for the session's turn, destroys the session's `bt-<tag>-*` apps, removes the containers with their volumes and the networks of those apps and of the brick's examples unless another session kept one running, the images of the tag pulled on the board, the images its apps pulled by themselves, and the assets folder last, then lists what remains. Data written inside shipped example folders is reported, not deleted: tell the user. On the host remove `bt-*` folders and any worktree the prompt created. Then decide on the registry tag, once every board the session used is cleaned:
   - A session tag exists only for this session: its name starts with `bt-`, given by the tool to a detached worktree or chosen by you. `buddy_prune` it when all planned tests are green.
   - A tag named after a branch stays, the branch may be tested again. The tool drops it 3 days after its last push.
   - After a failure keep the tag, whatever its kind: the rerun needs it. Prune it when the user says the session is over.

## Evidence contract for the report

State the board and tag, the revision and the dev image digests the containers ran, each check with its marker, each example with the line that proves it worked, what hardware was faked or not covered, what cleanup left on purpose, and the pre-existing failures met with how they were proven pre-existing.

## Board descriptors

`boards/*.yaml` describe board models: features with their `check` commands, `platform_key` selecting the `brick_compose.<platform_key>.yaml` variant, storage thresholds. Add a model by copying `boards/TEMPLATE.yaml` in the `arduino-test-buddy` repository, under `plugin/skills/board-test/boards/`: an installed copy is replaced on update. Record only verified facts, with the `scouted` date.
