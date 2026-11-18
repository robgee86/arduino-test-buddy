# Disk: what is stored where

The tool keeps state in exactly four places, each with a fixed name, so nothing can leak unseen. None of them holds anything that can't be rebuilt from a checkout.

| What | Where | Size | How to see it | How to get it back |
|---|---|---|---|---|
| Registry data | `~/Library/Caches/arduino-test-buddy/registry` on macOS, `~/.cache/arduino-test-buddy/registry` on Linux, `%LocalAppData%\arduino-test-buddy\registry` on Windows | compressed images, about a third of their size | `arduino-test-buddy status`, or `du -sh` | tags no push used for 3 days are dropped after every full push and on every `up` and `down`; `prune --tag <tag>` for one branch at once, `prune --all` for everything |
| Build cache | the `arduino-test-buddy` buildx builder, in the Docker volume `buildx_buildkit_arduino-test-buddy0_state` | uncompressed layers, up to about twice the images | `docker buildx du --builder arduino-test-buddy` | entries no push used for 3 days are dropped after every full push and on every `up` and `down`; `prune --cache` empties it, `prune --all` removes the builder |
| Pulled images on a board | the board's Docker: the dev images, named `localhost:<port>/app-bricks/<image>:<tag>`, and the images the apps pulled by themselves | what the session's apps used | `preflight --tag <tag>`, `cleanup --dry-run` | `cleanup --tag <tag>` |
| Record of pulled images on a board | `/home/arduino/.cache/arduino-test-buddy/bt-<tag>.pulled`, one image per line | a few lines | `cleanup --dry-run` | `cleanup --tag <tag>` removes it after its images |

## The registry

A `registry:3` container named `arduino-test-buddy-registry`, bound to `127.0.0.1:5005`, created by the first `up` and running only between `up` and `down`, never restarted by Docker on its own. Its data is a plain folder bind-mounted into it, written as your user, so `du` measures it and deleting it needs no root. The registry's own catalog is the only record of what it holds; the tool keeps no state file that could drift from it. A tag lives 3 days from its last push: after every full push and on every `up` and `down` the tool deletes the tags no push touched for 3 days and frees their layers, as it does for the build cache, so a forgotten branch gives its space back by itself. Only a push dates a tag, a board run does not. `status` lists every tag with its last push time, oldest first, and marks the ones the next cleanup point drops as stale.

`prune --tag <tag>` deletes the tag from every repository, then runs the registry's garbage collection, which frees the layers no other tag uses, and restarts the registry: it caches in memory which layers exist, and without the restart a later push would skip uploading layers that were just deleted. A manifest shared with another tag keeps that tag. `prune --all` removes the container, the builder and the whole cache folder, lock file included.

## The build cache

Builds run on a dedicated builder, which buildx runs in its own container, `buildx_buildkit_arduino-test-buddy0`, also running only between `up` and `down`. Its cache holds only this tool's builds and pruning it never touches other projects. The cache is what keeps rebuilds fast and layers identical between pushes, so only changed layers ever reach the registry and the boards.

When a full push ends, and on every `up` and `down`, the tool drops every cache entry no push has used for 3 days. A full push rebuilds every container, so each push refreshes everything its branch still needs: branches tested in parallel keep their layers, and a branch nobody pushed for 3 days ages out. A push narrowed with `--targets` does not prune, and a push or an `up` that finds another push or a board run in progress leaves pruning to the next cleanup point. The tags follow the same rule at the same points. Since every use of the tool is a cleanup point, a cache left alone for years is reclaimed the first time the tool runs again; if it never runs again, the cache stays in its one volume until `prune --all`.

## On a board

The board stores only the images its apps pulled: the dev images under the session tag, and the images an app pulls by itself, such as the database a brick runs. Each `run` compares the board's images before and after the start and records the new ones for the session; sessions take turns, so a new image is one this app asked for. `cleanup` removes the dev images and the recorded ones with the session's apps, containers, networks and assets folder, and never an image the board held before. Nothing of the registry or the builder ever lives on a board.

## Concurrency

Pushes share a lock on the cache folder; pruning takes it alone and waits for running pushes to end. The lock is a kernel file lock, released when the process exits for any reason, so it can't go stale.
