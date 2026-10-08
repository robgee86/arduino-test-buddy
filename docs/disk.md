# Disk: what is stored where

The tool keeps state in exactly three places, each with a fixed name, so nothing can leak unseen. None of them holds anything that can't be rebuilt from a checkout.

| What | Where | Size | How to see it | How to get it back |
|---|---|---|---|---|
| Registry data | `~/Library/Caches/arduino-test-buddy/registry` on macOS, `~/.cache/arduino-test-buddy/registry` on Linux | compressed images, about a third of their size | `arduino-test-buddy registry`, or `du -sh` | `prune --tag <tag>` for one branch, `prune --all` for everything |
| Build cache | the `arduino-test-buddy` buildx builder, in the Docker volume `buildx_buildkit_arduino-test-buddy0_state` | uncompressed layers, up to about twice the images | `docker buildx du --builder arduino-test-buddy` | trimmed after every full push; `prune --cache` empties it, `prune --all` removes the builder |
| Pulled images on a board | the board's Docker, named `localhost:<port>/app-bricks/<image>:<tag>` | what the session's apps used | `preflight --tag <tag>` | `cleanup --tag <tag>` |

## The registry

A `registry:3` container named `arduino-test-buddy-registry`, bound to `127.0.0.1:5005`, started by the first `push` or `registry` call and restarted with Docker. Its data is a plain folder bind-mounted into it, written as your user, so `du` measures it and deleting it needs no root. The registry's own catalog is the only record of what it holds; the tool keeps no state file that could drift from it.

`prune --tag <tag>` deletes the tag from every repository, then runs the registry's garbage collection, which frees the layers no other tag uses, and restarts the registry: it caches in memory which layers exist, and without the restart a later push would skip uploading layers that were just deleted. A manifest shared with another tag keeps that tag. `prune --all` removes the container, the builder and the whole cache folder, lock file included.

## The build cache

Builds run on a dedicated builder, so its cache holds only this tool's builds and pruning it never touches other projects. The cache is what keeps rebuilds fast and layers identical between pushes, so only changed layers ever reach the registry and the boards.

A full push builds every container, so it touches every cache entry still needed. When it ends, the tool drops every entry the push did not use: by definition the cache of images no longer built. A push narrowed with `--targets` does not prune, and a push that ends while another push is running leaves pruning to that one.

## On a board

The board stores only the images its apps pulled, under the session tag. `cleanup` removes them with the session's apps, containers, networks and assets folder. Nothing of the registry or the builder ever lives on a board.

## Concurrency

Pushes share a lock on the cache folder; pruning takes it alone and waits for running pushes to end. The lock is a kernel file lock, released when the process exits for any reason, so it can't go stale.
