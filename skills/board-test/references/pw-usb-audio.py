# Exit 0 if a USB audio node of the given class is plugged, as Microphone(0)/Speaker(0) see it.
# Usage from the host: ssh <board> python3 - Audio/Source < references/pw-usb-audio.py  (or Audio/Sink)
import json
import os
import subprocess
import sys
import time

media_class = sys.argv[1]
env = {**os.environ, "XDG_RUNTIME_DIR": f"/run/user/{os.getuid()}"}


def props(obj):
    return (obj.get("info") or {}).get("props") or {}


def usb_nodes():
    objects = json.loads(subprocess.run(["pw-dump"], capture_output=True, text=True, env=env, check=True).stdout)
    devices = {o["id"]: o for o in objects if props(o).get("media.class") == "Audio/Device"}
    return [
        props(o)["node.name"]
        for o in objects
        if props(o).get("media.class") == media_class and props(devices.get(props(o).get("device.id"), {})).get("device.bus") == "usb"
    ]


# A PipeWire session just started by the SSH login may not list the devices yet
for _ in range(5):
    usb = usb_nodes()
    if usb:
        break
    time.sleep(1)
print("\n".join(usb) or f"no USB {media_class} plugged")
sys.exit(0 if usb else 1)
