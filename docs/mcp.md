# MCP server

`arduino-test-buddy mcp` serves the operations as tools over stdio. The tools say what each call does; the `board-test` skill says how a board test session goes: the hard rules, the workflow from board choice to cleanup, the evidence a verdict needs, and the descriptors of the board models. An agent needs both.

## Claude Code plugin

This repository is itself a Claude Code plugin that bundles the two: its manifest in [.claude-plugin/](../.claude-plugin) declares the MCP server, and Claude Code loads the skill in [skills/board-test](../skills/board-test) only when a board test is asked for. The repository holds no catalog of its own: the [arduino-claude-plugins](https://github.com/robgee86/arduino-claude-plugins) catalog lists it, with this repository as the plugin's source.

```console
$ claude plugin marketplace add robgee86/arduino-claude-plugins
$ claude plugin install arduino-test-buddy@arduino-claude-plugins
```

Installing the plugin is all it takes: the plugin brings the program too. Its server entry starts [scripts/arduino-test-buddy.sh](../scripts/arduino-test-buddy.sh), which runs the program of the plugin's version from the plugin's data folder. When that program is missing, on the first start and after each plugin update, the script downloads the release archive for the machine from GitHub, refuses it unless it matches the release's checksums, unpacks it, and drops the older versions. This takes a few seconds once and needs `curl` or `wget`, and network access to GitHub. The program is never looked up on the `PATH`, so a copy installed by hand plays no part. A fetch gets 20 seconds, since Claude Code gives a server 30 to start; `ARDUINO_TEST_BUDDY_FETCH_SECONDS` in the server's `env` changes that. When an update cannot be fetched, the launcher runs the newest version it kept, says so on stderr and tries again at the next start; until then the new skill works with the older program. A first start has no kept version: if its download fails, the server does not start, and Claude Code then leaves a server that failed alone for 15 minutes, also in new sessions, so the server comes back by itself a quarter of an hour later.

The plugin carries the version of the release it was cut from, so `claude plugin update` brings the skill and the program of each new release together. Claude Code lists the plugin's server as `plugin:arduino-test-buddy:board` and names its tools `mcp__plugin_arduino-test-buddy_board__<tool>`: permission rules take that name. The server is called `board` there because a tool name holds 64 characters at most, prefix included. A server registered by hand under another name runs next to the plugin's and doubles every tool, so keep one of the two: `claude mcp remove arduino-test-buddy --scope user` drops the one added by hand.

The launcher is a shell script, so the plugin's server needs `sh`. On native Windows that means the `bin` folder of Git for Windows on the `PATH`; without it, register the server by hand as below.

## By hand

Without the plugin, or with another agent, install the two parts yourself, after the program, which then has to be on the `PATH`.

- The server: `claude mcp add --scope user arduino-test-buddy -- arduino-test-buddy mcp` for Claude Code, or the same command registered as a stdio server in any MCP client.
- The skill: copy [skills/board-test](../skills/board-test) into the agent's skills folder, `~/.claude/skills/` for Claude Code. It is a plain folder, a `SKILL.md` with the files it links. An agent without skills can be given the `SKILL.md` as instructions.

## Settings of the developer

`~/.config/arduino-test-buddy/config.yaml` holds what a developer sets once. Today it has one key, `source`, the `app-bricks-py` checkout `buddy_push` builds when no source is given and the session's folder is not a checkout:

```yaml
source: ~/Code/app-bricks-py
```

The file is read at every push, so an edit applies at once. An unknown key is an error.

## Boards of the developer

The skill picks a board from `~/.config/arduino-test-buddy/boards.yaml`: the SSH alias of each board the developer can reach, its RAM variant and the peripherals at hand. Copy [boards.example.yaml](../skills/board-test/boards.example.yaml) there and edit it, or let the first session ask for your board and write the file. It lives outside the repository, whose installed copy is replaced on every update.

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

The server ends when the client closes its end or sends it an interrupt or a terminate signal. Either way the running calls are cancelled, and the server exits once they return, or 2 seconds later. A cancelled `buddy_push` ends its build with every process the build started; on Windows only the build command itself ends.

## Timeouts

`buddy_push` on a machine without a warm build cache, `board_run` on a first start that pulls a runner image, and either board tool queueing behind another session's turn take minutes. Raise the MCP tool timeout of the client for those calls; the tool itself has no limit besides the run's own `timeout_seconds`.

## Images loaded by hand

Images loaded straight into the board's Docker with `docker save | ssh docker load` still work. Name them under `dev.local/`, a registry that never resolves so a missing image fails instead of pulling a release, and pass `--registry dev.local/` (or the `registry` field) to every call. Runners the brick references but you did not load must be retagged on the board from a released version under the same prefix and tag, `models-downloader` included for bricks with a model; cleanup removes them with the tag.
