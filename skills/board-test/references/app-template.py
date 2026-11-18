# Self-verifying board-test app: copy to python/main.py of a bt-* app and write the checks.
import traceback

from arduino.app_utils import App

results: list[bool] = []


def check(name, fn):
    """Run one check, print its marker and keep going on failure."""
    try:
        fn()
        print(f"BOARD-TEST PASS: {name}", flush=True)
        results.append(True)
    except Exception:
        print(f"BOARD-TEST FAIL: {name}\n{traceback.format_exc()}", flush=True)
        results.append(False)


def expect_raises(fn, error: type[Exception], message: str):
    """Assert fn raises exactly this error type and message."""
    try:
        fn()
    except error as e:
        assert str(e) == message, f"unexpected message: {e}"
        return
    raise AssertionError(f"no {error.__name__} raised")


# Bricks under test: App.start_brick(b) before use, App.stop_brick(b) after, one session at a time.
# check("happy path", lambda: ...)
# check("rejects bad input", lambda: expect_raises(lambda: ..., TypeError, "..."))

print(f"BOARD-TEST SUMMARY: {sum(results)}/{len(results)}", flush=True)

App.run()
