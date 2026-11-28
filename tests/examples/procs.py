"""Test-only process helper: start owned processes, wait for readiness, stop exactly those PIDs."""
import json
import os
import shutil
import signal
import subprocess
import time
import urllib.request

OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))
GATEWAY = "@tbrandenburg/opencode-a2a-lite@^0.9.0"


def get(url, timeout=3):
    with OPENER.open(url, timeout=timeout) as r:
        return json.load(r)


def clean_env(**extra):
    keys = ("PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "USER", "LOGNAME", "GOMODCACHE", "GOCACHE")
    env = {k: os.environ[k] for k in keys if k in os.environ}
    env["NO_PROXY"] = "127.0.0.1,localhost"
    return dict(env, **extra)


def gateway_command():
    if shutil.which("opencode-a2a-lite"):
        return ["opencode-a2a-lite"]
    return ["npx", "--yes", "--package=" + GATEWAY, "opencode-a2a-lite"]


class Procs:
    def __init__(self, logdir):
        self.logdir, self.owned = logdir, []

    def spawn(self, name, command, env=None, cwd=None):
        with open(os.path.join(self.logdir, name + ".log"), "wb") as log:
            proc = subprocess.Popen(command, env=env or clean_env(), cwd=cwd, stdout=log,
                                    stderr=subprocess.STDOUT, start_new_session=True)
        self.owned.append((name, proc))
        return proc

    def wait_url(self, name, proc, url, seconds):
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            if proc.poll() is not None:
                path = os.path.join(self.logdir, name + ".log")
                with open(path, errors="replace") as log:
                    tail = "".join("\n  log: " + line.rstrip()[:300] for line in log.readlines()[-15:])
                raise RuntimeError(name + " exited; see " + path + tail)
            try:
                return get(url)
            except (OSError, ValueError):
                time.sleep(.3)
        raise TimeoutError(name + " not ready at " + url)

    def stop(self):
        # Signal each owned process group: an npx wrapper would otherwise orphan the real gateway.
        for _, p in reversed(self.owned):
            if p.poll() is None:
                os.killpg(p.pid, signal.SIGTERM)
        for _, p in reversed(self.owned):
            try:
                p.wait(timeout=15)
            except subprocess.TimeoutExpired:
                os.killpg(p.pid, signal.SIGKILL)
                p.wait()


def private_policies(src, run):
    """The gateway rejects policy files that are group-writable or inside group-writable directories."""
    dst = os.path.join(run, "policies")
    shutil.copytree(src, dst)
    os.chmod(dst, 0o700)
    for name in os.listdir(dst):
        os.chmod(os.path.join(dst, name), 0o600)
