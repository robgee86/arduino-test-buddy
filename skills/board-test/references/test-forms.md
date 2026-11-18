# Other test forms

Apps are the default form. These cover what an app cannot, all through the tool so the session stays owned and serialized.

## Host scripts

Ad-hoc Python on the board's host OS: `board_shell` with a heredoc writing `/home/arduino/bt-<name>.py`, then `python3 /home/arduino/bt-<name>.py`. Remove the file at cleanup, `board_cleanup` does not know about it.

## In-container scripts

Run a script inside the session's Python image, mounted read-only, through `board_shell`. The image is on the board once a `board_run` of the session pulled it; `docker images | grep <tag>` gives its exact name, which carries the tunnel port of that run:

```sh
docker run --rm -v /home/arduino/bt-<name>.py:/t.py:ro --entrypoint python localhost:<port>/app-bricks/python-apps-base:<tag> /t.py
```

Add the devices, volumes and network of the relevant `brick_compose*.yaml` when the script needs hardware or a runner. To poke a running app use `board_exec` on one of its containers, named in the run result.

## Evidence from a running app

- The runner's data flow: `board_exec` on the runner container, `cat /proc/net/tcp` shows established connections to its port, or `docker logs --since 1m <runner>` through `board_shell`.
- A loop that prints nothing: CPU time and restart count from `docker stats --no-stream` and `docker inspect`.
- A Streamlit page: Playwright from the host against the board's port, Chrome channel, counting test ids; see `pw-usb-audio.py` for the pattern.
- Brick log lines: `arduino.app_utils.Logger` is not registered with `logging.getLogger`, assert on the printed lines instead.

## Service probes

To exercise a singleton service (`arduino:genie_audio`, ...) without an app, start it from its definition in the active assets folder under a `bt-` compose project, through `board_shell`:

```sh
docker compose -f /var/lib/arduino-app-cli/assets/<tag>/services/<org>/<service>/service_compose.yaml -p bt-<name> up -d
for i in $(seq 1 30); do curl -sf http://127.0.0.1:<port>/<health-path> >/dev/null && break; sleep 2; done
```

Drive its API with `curl`, then `docker compose -p bt-<name> down` at cleanup; the project's containers and network start with `bt-`, so `board_cleanup` removes strays.

## Images without the registry

When the host registry is not an option, build locally with `BRICKS_RELEASE_VERSION=<tag> task build:bricks` and `REGISTRY=dev.local/ IMAGE_TAG=<tag> BASE_IMAGE_VERSION=<tag> task build:containers -- python-apps-base <runner>...`, move them with `docker save <image> | gzip -1 | ssh <board> docker load`, and pass `--registry dev.local/` to every tool call. Runners the brick references but you did not build must be retagged on the board from a released version under `dev.local/app-bricks/<runner>:<tag>`, `models-downloader` included for bricks with a model; `board_cleanup` removes them with the tag.

## Template gaps to know

- `App.start_brick` swallows exceptions from `start()`: test a start error with a direct call. Extra brick instances need `App.unregister` or `App.run()` starts them again.
- `App._stop` stops a brick before its loop thread, so camera bricks report a `CameraReadError` at shutdown: snapshot errors before stopping.
- `Camera()` without an index may open a different device than the examples do; `Camera("usb:0")` is deterministic.
