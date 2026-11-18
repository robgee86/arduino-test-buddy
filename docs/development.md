# Development

```console
$ task check            # gofmt, vet, tests
$ task build            # dist/arduino-test-buddy
$ task install          # into GOPATH/bin, with asdf run `asdf reshim golang` afterwards
$ task mcp:add          # register the installed binary in Claude Code
$ task plugin:check     # validate the Claude Code plugin manifest, needs the claude CLI
$ task release:snapshot # every platform locally, needs goreleaser
```

Tests use a fake SSH runner, a fake command runner and an in-process fake registry, and never touch a board, Docker or the network. They pin the behavior that matters on shared boards and a shared cache: the exact cleanup commands for a session, turns taken by run and cleanup, the tunnel port used for pulls, the dev variables present on run calls and absent on cleanup calls, the early exit when a container dies, pruning only after a full push and never during another one, the tags not pushed for 3 days dropped with the cache and the pushed one kept, the images an app pulled recorded by run and removed by cleanup, the server ending on a stop while a request is still open, a cancelled command returning at once and taking the processes it started with it.

## Windows

The binaries are built for macOS, Linux and Windows. On Windows, WSL runs the Linux build with nothing special. The native build is experimental: it compiles and its tests pass in CI, but nobody has run a full session with it, since that takes a Windows machine with Docker Desktop and a board.

What CI runs on a Windows runner:

- Every unit test, among them the cache lock, which is a `LockFileEx` lock there with the same rules as `flock`, the tag listing over Windows paths, and the archive of an app folder, built in Go so the copy to a board needs neither `tar` nor a shell.
- The tests of the real `ssh` client against a real Linux `sshd`, which runs in a WSL distribution on the runner: the scripts the tool sends arrive intact, a folder copy lands as sent, two turns queue, and the tunnel reaches a port of the developer machine only for the turn.

What CI cannot run there, because the hosted Windows runners have no Linux containers: the registry and the builder on Docker Desktop, the build tasks of `app-bricks-py`, and anything on a real board.

The same `ssh` tests run on the Linux runner against its own `sshd`, and against any machine: `BUDDY_TEST_SSH_TARGET=<ssh alias> go test ./internal/board -run TestSSH` needs a Linux target with `flock`, `tar` and `curl`, a board included.

## Layout

- `internal/board` holds the board operations, each a method returning one result struct, over a `Runner` interface implemented by the system `ssh` so aliases, keys and jumps from the user's config apply. The runner also takes the board's turns.
- `internal/host` holds the developer machine side: the registry, the builder, push and prune, over a `Commander` interface for the child processes.
- `internal/cli` and `internal/mcpserver` are thin entry points over those methods; the CLI renders text or JSON, the MCP server registers each as a tool.
- `internal/shell` quotes command lines so no caller ever escapes by hand.
- `skills/board-test` is the skill, with its references and board descriptors, at the top level since no agent owns it. `.claude-plugin` makes the repository a Claude Code plugin around it: `plugin.json` is the manifest, with the MCP server entry, which starts `scripts/arduino-test-buddy.sh`: the launcher that fetches the released program of the plugin's version into the plugin's data folder and runs it. Its tests run it against a fake release, also one that fails or never answers, and CI also runs it against the real one. The catalog that offers the plugin is a repository of its own, [arduino-claude-plugins](https://github.com/robgee86/arduino-claude-plugins), which names this one as the plugin's source. Claude Code copies the whole repository on install. `claude --plugin-dir .` loads the working copy for one session.

## CI and releases

`check.yml` runs `task check` on every push to `main` and on pull requests, on a Linux and a Windows runner, then the tests of the real `ssh` client against an `sshd` it starts on each.

`release.yml` runs on every push to `main`. [release-please](https://github.com/googleapis/release-please) reads the Conventional Commits since the last release and keeps a release pull request up to date with the next version and the changelog; the first release is `0.1.0`, and while in the 0.x range `feat` commits bump the minor version, `fix` commits the patch, and a breaking change bumps the minor version too, so the 1.0.0 release happens only when you ask for it with a `Release-As: 1.0.0` footer. Merging that pull request creates the tag and the GitHub release, and the same workflow then runs goreleaser on the tag to build the binaries for macOS and Linux and attach the archives and checksums. Both steps live in one workflow because a tag created with the workflow token triggers no other workflow.

The release pull request also writes the new version into `.claude-plugin/plugin.json`, which is what tells Claude Code that the plugin has an update; a test fails when the two versions differ.

Commit messages therefore decide the version: `feat:` for a new capability, `fix:` for a correction, `feat!:` or a `BREAKING CHANGE:` footer for an incompatible change, anything else leaves the version alone.
