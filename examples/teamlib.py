"""Scaffolding shared by the team examples: process control, readiness, policies, evidence.

Operator-side only; it makes no collaboration decisions and is not part of the middleware.
"""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import time
import urllib.request

ID = re.compile(r"[a-z0-9][a-z0-9_-]{0,63}")
READ_TOOLS = ("discover_agents", "list_collections", "get_agent_health", "get_delivery",
              "list_state", "get_state", "put_state", "note", "read_notes")
DELEGATE_TOOLS = ("ask", "send_message", "broadcast", "dispatch")
TERMINAL = {"completed", "failed", "partial", "uncertain", "input-required", "auth-required"}
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))
UNITS = {"ms": 1, "s": 1000, "m": 60000, "h": 3600000}


def get(url, timeout=3):
    with OPENER.open(url, timeout=timeout) as r:
        return json.load(r)


def clean_environment():
    # Do not pass ambient provider keys, AGENTIPLEX settings, proxy or runtime overrides.
    keys = ("PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "USER", "LOGNAME")
    env = {k: os.environ[k] for k in keys if k in os.environ}
    env["NO_PROXY"] = "127.0.0.1,localhost"
    return env


def opencode_supported(version):
    match = re.fullmatch(r"opencode v(\d+)\.(\d+)\.(\d+)", version)
    return bool(match) and tuple(map(int, match.groups())) >= (2, 3, 2)


def duration_ms(value):
    match = re.fullmatch(r"([0-9]{1,10})(ms|s|m|h)", value)
    return int(match.group(1)) * UNITS[match.group(2)] if match else None


def valid_prompt_timeout(value):
    if value == "unlimited":
        return value
    ms = duration_ms(value)
    if ms is not None and 30 <= ms <= 2147483647:
        return value
    raise argparse.ArgumentTypeError("prompt timeout must be 30ms–2147483647ms, or unlimited")


def policy(profile, tools):
    return {"version": 1, "base": profile, "policies": [
        {"action": "mcp.use", "resource": "agentiplex", "effect": "allow"},
        *[{"action": "tool.use", "resource": "agentiplex_" + t + ":*", "effect": "allow"} for t in tools]]}


def call_evidence(deliveries, coordinator):
    """Durable proof of the coordinator's delegations (gateway logs only show policy grants).

    Every non-coordinator delivery exists because someone submitted it; with per-agent credentials
    its `caller` is the authenticated submitter. The record does not store which tool was used, so
    `ask` cannot be told apart from `send_message`/`dispatch`/`broadcast`; recipient execution alone
    is not tool evidence. Completed deliveries are counted separately from invocations.
    """
    sent = [d for d in deliveries if d["target"] != coordinator]
    count = lambda items: {t: sum(d["target"] == t for d in items) for t in sorted({d["target"] for d in items})}
    return {"coordinatorTurns": sum(d["target"] == coordinator for d in deliveries),
            "delegations": count(sent), "delegationCalls": len(sent),
            "delegationsCompleted": count([d for d in sent if d["state"] == "completed"]),
            "callers": sorted({d.get("caller") or "unauthenticated" for d in sent}),
            "toolIdentity": "not recorded (ask is not distinguishable from other delegation tools)"}


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def registry(namespace, urls, groups=None, pools=None):
    """Native registry YAML fragment (JSON is YAML) generated from the roster and bound endpoints."""
    return {"namespace": namespace, "agents": {name: {"url": url} for name, url in urls.items()},
            "groups": groups or {}, "pools": pools or {}}


class Team:
    def __init__(self, args, team, root, deadline):
        self.args, self.team, self.root, self.deadline = args, team, root, deadline
        self.endpoint = "http://127.0.0.1:" + str(args.port)
        self.children, self.daemon_spec, self.daemon_restarts = [], None, 0
        self.env = clean_environment()

    def remaining(self):
        n = self.deadline - time.monotonic()
        if n <= 0:
            raise TimeoutError("team deadline exceeded; stopping owned processes")
        return n

    def spawn(self, name, command, env, cwd=None, append=False):
        with (self.root / (name + ".log")).open("ab" if append else "wb") as log:
            proc = subprocess.Popen(command, env=env, cwd=cwd, stdout=log,
                                    stderr=subprocess.STDOUT, start_new_session=True)
        entry = (name, proc)
        self.children = [entry if c[0] == name else c for c in self.children] if any(
            c[0] == name for c in self.children) else self.children + [entry]
        return proc

    def alive(self):
        self.remaining()
        for name, p in list(self.children):
            if p.poll() is None:
                continue
            if name != "daemon" or not getattr(self.args, "restart_daemon", False):
                raise RuntimeError(name + " exited; inspect private log")
            # Stands in for the operator/supervisor restart policy (docs/DEPLOYMENT.md): same
            # command, same storage, same port. Agents re-attach by repeating their ask.
            self.daemon_restarts += 1
            print("Daemon exited; restarting on the same storage and port", flush=True)
            self.spawn("daemon", *self.daemon_spec, append=True)
            self.wait_daemon()

    def start_daemon(self, namespace, dashboard, urls, groups=None, pools=None):
        """Start the daemon last, from generated native config: endpoints already exist and are registered at startup."""
        config = {"server": {"listen": "127.0.0.1:" + str(self.args.port)},
                  "broker": {"mode": "embedded", "storage": str(self.root / "jetstream")},
                  "protocols": {"mcp": {"enabled": True}},
                  "observability": {"dashboard": dashboard},
                  # The delivery timeout bounds one HTTP operation to a gateway, not agent execution.
                  "delivery": {"timeout": "30s", "retry_delay": "2s", "max_attempts": 80},
                  "registry": registry(namespace, urls, groups, pools)}
        (self.root / "daemon.yaml").write_text(json.dumps(config, indent=2) + "\n")
        command = [str(self.root / "agentiplexd"), "--config", str(self.root / "daemon.yaml")]
        self.daemon_spec = (command, self.env)
        self.spawn("daemon", command, self.env)
        self.wait_daemon()

    def wait_daemon(self):
        while True:
            self.alive_basic()
            try:
                if get(self.endpoint + "/readyz").get("ready"):
                    return
            except (OSError, ValueError):
                pass
            time.sleep(.2)

    def alive_basic(self):
        self.remaining()
        for name, p in self.children:
            if p.poll() is not None:
                raise RuntimeError(name + " exited; inspect private log")

    def stop(self):
        # Signal only owned process groups; the gateway handles its private ACP cleanup.
        for _, p in reversed(self.children):
            if p.poll() is None:
                try:
                    p.terminate()
                except ProcessLookupError:
                    pass
        end = time.monotonic() + 15
        for _, p in reversed(self.children):
            try:
                p.wait(timeout=max(.1, end - time.monotonic()))
            except subprocess.TimeoutExpired:
                if p.poll() is None:
                    os.killpg(p.pid, signal.SIGKILL)
                p.wait()

    def cli(self, *args):
        self.alive()
        raw = subprocess.check_output([str(self.root / "agentiplex"), "--server", self.endpoint, *args],
                                      env=self.env, timeout=min(20, self.remaining()))
        return json.loads(raw)

    def wait_card(self, url, proc, name, skill):
        while self.remaining() > 0:
            if proc.poll() is not None:
                raise RuntimeError(name + " startup failed; inspect private log")
            try:
                card = get(url + "/.well-known/agent-card.json")
            except (OSError, ValueError):
                time.sleep(.3)
                continue
            if card.get("name") != name:
                raise RuntimeError("unexpected agent card identity at " + url)
            if not any(skill in s.get("tags", []) or s.get("id") == skill for s in card.get("skills", [])):
                raise RuntimeError("missing advertised role skill/tag: " + name)
            return card

    def gateway_ready(self, name, proc):
        # The pinned gateway chooses its own free port; accept only its own readiness record.
        path = self.root / (name + ".log")
        while self.remaining() > 0:
            if proc.poll() is not None:
                raise RuntimeError(name + " startup failed; inspect private log")
            with path.open(errors="replace") as f:
                for line in f:
                    if not line.startswith("{"):
                        continue
                    try:
                        item = json.loads(line)
                    except ValueError:
                        continue
                    if item.get("ready") is True:
                        url = item.get("url", "").rstrip("/")
                        if not re.fullmatch(r"http://127\.0\.0\.1:[0-9]{1,5}", url):
                            raise RuntimeError("unexpected gateway URL")
                        return url
            time.sleep(.3)

    def details(self, root_id, records):
        """Authoritative status of the root, every observed delivery and broadcast children."""
        parent = self.cli("messages", "status", root_id)
        found = {d["id"]: self.cli("messages", "status", d["id"]) for d in records}
        found[root_id] = parent
        for d in list(found.values()):
            for c in d.get("children") or []:
                found.setdefault(c["id"], self.cli("messages", "status", c["id"]))
        return parent, list(found.values())
