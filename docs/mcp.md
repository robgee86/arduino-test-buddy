# MCP server

`arduino-test-buddy mcp` serves the operations as tools over stdio. Register it once at user scope so every project can use it:

```console
$ claude mcp add --scope user arduino-test-buddy -- arduino-test-buddy mcp
```

Defaults for the board, tag, registry and turn wait can be baked into the registration (`-- arduino-test-buddy --board ventunoq mcp`); the board tools also accept `board`, `tag` and `registry` as optional fields, and `board_push` and `board_prune` take `tag`.

| Tool | Operation | Notes |
|---|---|---|
| `board_preflight` | preflight | read-only |
| `board_push` | push | on the developer machine; minutes on the first build of a machine |
| `board_registry` | registry | on the developer machine; starts the registry once |
| `board_prune` | prune | on the developer machine; destructive, scoped to the tool's registry and builder |
| `board_examples` | examples | read-only |
| `board_run` | run | queues for the board's turn, then blocks until the marker or the timeout |
| `board_logs` | logs | read-only |
| `board_exec` | exec | shell inside a container |
| `board_shell` | shell | shell on the board's host, not for `arduino-app-cli` |
| `board_cleanup` | cleanup | destructive, scoped to the session; `dry_run` first |

Results are structured content, the same JSON `--format json` prints. Errors of the operation itself, such as a failed start, come back inside the result so the agent can read them; only unreachable boards and malformed input are tool errors.

## Timeouts

`board_push` on a machine without a warm build cache, `board_run` on a first start that pulls a runner image, and either board tool queueing behind another session's turn take minutes. Raise the MCP tool timeout of the client for those calls; the tool itself has no limit besides the run's own `timeout_seconds`.

## Images loaded by hand

Images loaded straight into the board's Docker with `docker save | ssh docker load` still work. Name them under `dev.local/`, a registry that never resolves so a missing image fails instead of pulling a release, and pass `--registry dev.local/` (or the `registry` field) to every call. Runners the brick references but you did not load must be retagged on the board from a released version under the same prefix and tag, `models-downloader` included for bricks with a model; cleanup removes them with the tag.
