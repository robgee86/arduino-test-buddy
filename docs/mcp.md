# MCP server

`arduino-test-buddy mcp` serves the operations as tools over stdio. The tools say what each call does; the `board-test` skill says how a board test session goes: the hard rules, the workflow from board choice to cleanup, the evidence a verdict needs, and the descriptors of the board models. An agent needs both.

## Claude Code plugin

This repository is itself a Claude Code plugin that bundles the two: its manifest in [.claude-plugin/](../.claude-plugin) starts the installed binary as the MCP server, and Claude Code loads the skill in [skills/board-test](../skills/board-test) only when a board test is asked for.

```console
$ claude plugin marketplace add robgee86/arduino-test-buddy
$ claude plugin install arduino-test-buddy@arduino-test-buddy
```

The binary must be on the `PATH`, the plugin does not install it. The plugin carries the version of the release it was cut from, so `claude plugin update` brings the skill of each new release. Claude Code lists the plugin's server as `plugin:arduino-test-buddy:buddy` and names its tools `mcp__plugin_arduino-test-buddy_buddy__<tool>`: permission rules take that name. The server is called `buddy` there because a tool name holds 64 characters at most, prefix included. A server registered by hand with the same command takes precedence and the plugin's copy is not started, so keep one of the two: `claude mcp remove arduino-test-buddy --scope user` drops the one added by hand.

## By hand

Without the plugin, or with another agent, install the two parts yourself.

- The server: `claude mcp add --scope user arduino-test-buddy -- arduino-test-buddy mcp` for Claude Code, or the same command registered as a stdio server in any MCP client.
- The skill: copy [skills/board-test](../skills/board-test) into the agent's skills folder, `~/.claude/skills/` for Claude Code. It is a plain folder, a `SKILL.md` with the files it links. An agent without skills can be given the `SKILL.md` as instructions.

## Boards of the developer

The skill picks a board from `~/.config/arduino-test-buddy/boards.yaml`: the SSH alias of each board the developer can reach, its RAM variant and the peripherals at hand. Copy [boards.example.yaml](../skills/board-test/boards.example.yaml) there and edit it. It lives outside the repository, whose installed copy is replaced on every update.

## Tools

Defaults for the board, tag, registry and turn wait can be baked into the registration (`-- arduino-test-buddy --board ventunoq mcp`); the board tools also accept `board`, `tag` and `registry` as optional fields, and `buddy_push` and `buddy_prune` take `tag`. The `buddy_` tools act on the developer machine, the `board_` tools on a board.

| Tool | Operation | Notes |
|---|---|---|
| `buddy_up` | up | starts the registry and the builder, drops cache and tags not pushed for 3 days |
| `buddy_down` | down | waits for running pushes and runs, drops cache and tags not pushed for 3 days, then stops both |
| `buddy_status` | status | read-only, starts nothing |
| `buddy_push` | push | needs the tool up; minutes on the first build of a machine |
| `buddy_prune` | prune | destructive, scoped to the tool's registry and builder |
| `board_preflight` | preflight | read-only |
| `board_examples` | examples | read-only |
| `board_run` | run | queues for the board's turn, then blocks until the marker or the timeout |
| `board_logs` | logs | read-only |
| `board_exec` | exec | shell inside a container |
| `board_shell` | shell | shell on the board's host, not for `arduino-app-cli` |
| `board_cleanup` | cleanup | destructive, scoped to the session, the images its apps pulled included; `dry_run` first |

Results are structured content, the same JSON `--format json` prints. Errors of the operation itself, such as a failed start, come back inside the result so the agent can read them; only unreachable boards and malformed input are tool errors.

## Timeouts

`buddy_push` on a machine without a warm build cache, `board_run` on a first start that pulls a runner image, and either board tool queueing behind another session's turn take minutes. Raise the MCP tool timeout of the client for those calls; the tool itself has no limit besides the run's own `timeout_seconds`.

## Images loaded by hand

Images loaded straight into the board's Docker with `docker save | ssh docker load` still work. Name them under `dev.local/`, a registry that never resolves so a missing image fails instead of pulling a release, and pass `--registry dev.local/` (or the `registry` field) to every call. Runners the brick references but you did not load must be retagged on the board from a released version under the same prefix and tag, `models-downloader` included for bricks with a model; cleanup removes them with the tag.
