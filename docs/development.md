# Development

```console
$ task check            # gofmt, vet, tests
$ task build            # dist/arduino-test-buddy
$ task install          # into GOPATH/bin, with asdf run `asdf reshim golang` afterwards
$ task mcp:add          # register the installed binary in Claude Code
$ task release:snapshot # every platform locally, needs goreleaser
```

Tests use a fake SSH runner, a fake command runner and an in-process fake registry, and never touch a board, Docker or the network. They pin the behavior that matters on shared boards and a shared cache: the exact cleanup commands for a session, turns taken by run and cleanup, the tunnel port used for pulls, the dev variables present on run calls and absent on cleanup calls, the early exit when a container dies, pruning only after a full push and never during another one.

The binaries are built for macOS and Linux only: the cache lock relies on Unix file locks, and Windows developers use WSL.

## Layout

- `internal/board` holds the board operations, each a method returning one result struct, over a `Runner` interface implemented by the system `ssh` so aliases, keys and jumps from the user's config apply. The runner also takes the board's turns.
- `internal/host` holds the developer machine side: the registry, the builder, push and prune, over a `Commander` interface for the child processes.
- `internal/cli` and `internal/mcpserver` are thin entry points over those methods; the CLI renders text or JSON, the MCP server registers each as a tool.
- `internal/shell` quotes command lines so no caller ever escapes by hand.

## CI and releases

`check.yml` runs `task check` on every push to `main` and on pull requests.

`release.yml` runs on every push to `main`. [release-please](https://github.com/googleapis/release-please) reads the Conventional Commits since the last release and keeps a release pull request up to date with the next version and the changelog; until the first release, `feat` commits bump the minor version and `fix` commits the patch, starting from `0.1.0`. Merging that pull request creates the tag and the GitHub release, and the same workflow then runs goreleaser on the tag to build the binaries for macOS and Linux and attach the archives and checksums. Both steps live in one workflow because a tag created with the workflow token triggers no other workflow.

Commit messages therefore decide the version: `feat:` for a new capability, `fix:` for a correction, `feat!:` or a `BREAKING CHANGE:` footer for an incompatible change, anything else leaves the version alone.
