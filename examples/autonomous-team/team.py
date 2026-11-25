#!/usr/bin/env python3
"""Disposable example launcher/observer. No collaboration decisions or model API calls."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
ID = re.compile(r"[a-z0-9][a-z0-9_-]{0,63}")
READ_TOOLS = ("discover_agents", "list_collections", "get_agent_health",
              "get_delivery", "list_state", "get_state", "put_state")
DELEGATE_TOOLS = ("send_message", "broadcast", "dispatch")
TERMINAL = {"completed", "failed", "partial", "uncertain", "input-required", "auth-required"}
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def load_team(path, profile):
    team = json.loads(Path(path).read_text())
    if set(team) != {"agents", "groups", "pools"}:
        raise ValueError("expected agents, groups, pools")
    agents = team["agents"]
    if not isinstance(agents, list) or not 2 <= len(agents) <= 12:
        raise ValueError("expected 2–12 agents")
    ids = set()
    for a in agents:
        if set(a) != {"id", "runtime", "skill", "role"}:
            raise ValueError("invalid agent fields")
        if not isinstance(a["id"], str) or not ID.fullmatch(a["id"]) or a["id"] in ids:
            raise ValueError("invalid or duplicate ID")
        if a["runtime"] not in ("opencode", "simulated") or a["role"] not in ("pm", "architect", "developer", "tester", "quality"):
            raise ValueError("unknown runtime or role")
        if not isinstance(a["skill"], str) or not ID.fullmatch(a["skill"]):
            raise ValueError("invalid skill")
        if (a["id"] == "pm") != (a["role"] == "pm"):
            raise ValueError("pm is the only coordinator")
        ids.add(a["id"])
        if profile != "configured":
            a["runtime"] = "opencode" if profile == "real" else "simulated"
    if "pm" not in ids:
        raise ValueError("missing PM")
    for kind in ("groups", "pools"):
        cs = team[kind]
        if not isinstance(cs, dict) or len(cs) > 32:
            raise ValueError("invalid collections")
        for name, members in cs.items():
            if (not ID.fullmatch(name) or not isinstance(members, list) or
                    not 1 <= len(members) <= 32 or len(set(members)) != len(members)
                    or not set(members) <= ids - {"pm"}):
                raise ValueError("collections must contain unique registered workers, never pm")
    return team


def workspace(root, agent, endpoint, model, smoke=False):
    # No arbitrary source tree, inherited config, plugins, or local tool commands.
    project = root / agent["id"]
    project.mkdir(mode=0o700)
    shutil.copytree(HERE / "fixture", project / "fixture")
    tools = READ_TOOLS + (DELEGATE_TOOLS if agent["id"] == "pm" else ())
    if smoke and agent["id"] == "pm":
        tools = ("discover_agents", "send_message", "get_delivery", "put_state")
    elif smoke and agent["role"] == "architect":
        tools = ("put_state",)
    role_path = HERE / "roles" / (agent["role"] + ".md")
    if smoke and agent["role"] in ("pm", "architect"):
        role_path = HERE / "roles" / (agent["role"] + "-smoke.md")
    role = role_path.read_text()
    system = "Logical agent ID: " + agent["id"] + ". " + role
    permissions = [{"action": "*", "resource": "*", "effect": "deny"}]
    permissions += [{"action": name, "resource": "*", "effect": "allow"} for name in ("read", "glob", "grep")]
    permissions += [{"action": "agentiplex_" + tool, "resource": "*", "effect": "allow"} for tool in tools]
    config = {
        "$schema": "https://opencode.ai/config.json",
        "default_agent": agent["id"],
        "agents": {agent["id"]: {"mode": "primary", "description": agent["skill"],
                                 "system": system, "permissions": permissions}},
        "plugins": [], "skills": [], "instructions": [],
        "snapshots": False, "warming": False,
        "mcp": {"servers": {"agentiplex": {"type": "remote", "url": endpoint + "/mcp",
                                          "protocol": "auto", "codemode": False}}},
    }
    if model is not None:
        config["model"] = {"providerID": model[0], "model": model[1]}
    policy = {"version": 1, "base": "read-only", "policies": [
        {"action": "mcp.use", "resource": "agentiplex", "effect": "allow"},
        *[{"action": "tool.use", "resource": "agentiplex_" + tool + ":*", "effect": "allow"} for tool in tools]
    ]}
    (project / "opencode.json").write_text(json.dumps(config, indent=2) + "\n")
    (project / "AGENTS.md").write_text(system)
    path = root / (agent["id"] + "-policy.json")
    path.write_text(json.dumps(policy, indent=2) + "\n")
    return project, path


def get(url, timeout=3):
    with OPENER.open(url, timeout=timeout) as r:
        return json.load(r)


def opencode_supported(version):
    match = re.fullmatch(r"opencode v(\d+)\.(\d+)\.(\d+)", version)
    return bool(match) and tuple(map(int, match.groups())) >= (2, 3, 2)


def clean_environment():
    # Do not pass ambient provider keys, AGENTIPLEX settings, proxy or runtime
    # overrides to the bus or simulator.
    keys = ("PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "USER", "LOGNAME")
    env = {k: os.environ[k] for k in keys if k in os.environ}
    env["NO_PROXY"] = "127.0.0.1,localhost"
    return env


class Team:
    def __init__(self, args, team, root, deadline):
        self.args, self.team, self.root, self.deadline = args, team, root, deadline
        self.endpoint = "http://127.0.0.1:" + str(args.port)
        self.children = []
        self.env = clean_environment()

    def remaining(self):
        n = self.deadline - time.monotonic()
        if n <= 0:
            raise TimeoutError("team deadline exceeded; stopping owned processes")
        return n

    def spawn(self, name, command, env, cwd=None):
        with (self.root / (name + ".log")).open("wb") as log:
            proc = subprocess.Popen(command, env=env, cwd=cwd, stdout=log,
                                    stderr=subprocess.STDOUT, start_new_session=True)
        self.children.append((name, proc))
        return proc

    def alive(self):
        self.remaining()
        for name, p in self.children:
            if p.poll() is not None:
                raise RuntimeError(name + " exited; inspect private log")

    def stop(self):
        # Stop submitter/agents first, then daemon; signal only owned process
        # groups while their leader is alive. Gateway handles its private ACP cleanup.
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
        # The pinned gateway chooses a free port itself. Accept only its own
        # readiness record, never infer readiness from an arbitrary port.
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

    def start(self):
        self.spawn("daemon", [str(self.root / "agentiplexd"), "--listen",
                   "127.0.0.1:" + str(self.args.port), "--public-url", self.endpoint,
                   "--broker-mode", "embedded", "--namespace", "autonomous-demo",
                   "--storage", str(self.root / "jetstream"), "--mcp",
                   "--dashboard=" + str(not self.args.headless).lower()],
                   dict(self.env, AGENTIPLEX_DELIVERY_TIMEOUT="30s",
                        AGENTIPLEX_DELIVERY_RETRY_DELAY="2s", AGENTIPLEX_DELIVERY_MAX_ATTEMPTS="80"))
        while True:
            self.alive()
            try:
                if get(self.endpoint + "/readyz").get("ready"):
                    break
            except (OSError, ValueError):
                pass
            time.sleep(.2)
        for i, a in enumerate(self.team["agents"]):
            name, skill = a["id"], a["skill"]
            if a["runtime"] == "simulated":
                port = self.args.port + i + 1
                p = self.spawn(name, [str(self.root / "simulated-agent"), "--name", name,
                                       "--skill", skill, "--port", str(port)], self.env)
                url = "http://127.0.0.1:" + str(port)
            else:
                project, policy = workspace(self.root, a, self.endpoint, self.args.model.split("/", 1) if self.args.model else None, self.args.smoke)
                env = dict(self.env, A2A_NAME=name, A2A_DESCRIPTION="Read-only engineering " + skill,
                           A2A_TAGS=skill + ",real-opencode")
                for key in self.args.pass_env:
                    env[key] = os.environ[key]
                cmd = [self.args.gateway, "--workspace", str(project), "--port", "0",
                       "--policy", "read-only", "--permissions", "reject", "--policy-file", str(policy),
                       "--prompt-timeout", self.args.prompt_timeout]
                for key in self.args.pass_env:
                    cmd += ["--pass-env", key]
                p = self.spawn(name, cmd, env, project)
                url = self.gateway_ready(name, p)
            card = self.wait_card(url, p, name, skill)
            tags = {t for s in card.get("skills", []) for t in s.get("tags", [])}
            if ("simulated" in tags) != (a["runtime"] == "simulated"):
                raise RuntimeError("agent runtime label mismatch: " + name)
            self.cli("register", name, url)
            print("Registered", name, a["runtime"], url, flush=True)
        for kind in ("groups", "pools"):
            for name, members in self.team[kind].items():
                self.cli(kind, "set", name, *members)
        agents = self.cli("agents", "list")
        if {a["id"] for a in agents} != {a["id"] for a in self.team["agents"]}:
            raise RuntimeError("registry mismatch")
        if get(self.endpoint + "/api/v1/deliveries?limit=100"):
            raise RuntimeError("unexpected existing deliveries")
        print("Team ready; dashboard:", self.endpoint + "/ui/", flush=True)

    def snapshot(self, root_id):
        records = get(self.endpoint + "/api/v1/deliveries?limit=100")
        nested = [d for d in records if d["id"] != root_id]
        total = sum(len(d.get("children") or []) or 1 for d in nested)
        if len(nested) > 6 or total > 16:
            raise RuntimeError("observed delegation budget exceeded; stopping team")
        if any(d["target"] == "pm" or d.get("assignedAgent") == "pm" or
               any(c["target"] == "pm" for c in (d.get("children") or [])) for d in nested):
            raise RuntimeError("recursive PM delegation observed; stopping team")
        return records

    def observe(self, objective):
        # The one and only workflow submission by the launcher. Thereafter it
        # reads receipts and state; models choose all collaboration operations.
        root = self.cli("send", "pm", objective, "--idempotency-key", "autonomous-review")
        root_id = root["id"]
        previous = None
        waiting = None
        while True:
            self.alive()
            records = self.snapshot(root_id)
            summary = sorted((d["id"], d["target"], d["state"]) for d in records)
            if summary != previous:
                print("Observed:", json.dumps(summary), flush=True)
                previous = summary
            parent = self.cli("messages", "status", root_id)
            if parent["state"] in TERMINAL:
                # Snapshot actual evidence, including failed/intervention outcomes.
                records = self.snapshot(root_id)
                details_by_id = {d["id"]: self.cli("messages", "status", d["id"]) for d in records}
                details_by_id[root_id] = parent
                for d in list(details_by_id.values()):
                    for c in d.get("children") or []:
                        if c["id"] not in details_by_id:
                            details_by_id[c["id"]] = self.cli("messages", "status", c["id"])
                details = list(details_by_id.values())
                state = [self.cli("state", "get", "review", e["key"]) for e in self.cli("state", "list", "review")]
                evidence = {"classification": "live-model", "requestedModel": self.args.model or "OpenCode default",
                            "credentialMode": "explicit-provider" if self.args.pass_env else "credential-free",
                            "gatewayPromptTimeout": self.args.prompt_timeout,
                            "root": parent, "deliveries": details, "state": state,
                            "agents": self.team["agents"]}
                (self.root / "evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
                # A completed PM is not a completed team. Drain only already
                # admitted work, within the same deadline. Preserve intermediate
                # evidence if the deadline expires; never replay or cancel tasks.
                pending = sorted(d["id"] for d in details if d["state"] not in TERMINAL)
                if parent["state"] == "completed" and pending:
                    if pending != waiting:
                        print("PM completed; waiting for outstanding deliveries:", json.dumps(pending), flush=True)
                        waiting = pending
                    time.sleep(.5)
                    continue
                print("Terminal:", json.dumps(terminal_diagnostic(parent)), flush=True)
                print("Observed state keys:", json.dumps([{"key": s["key"], "revision": s["revision"]} for s in state]), flush=True)
                assessment = verify_evidence(evidence)
                # A worker completing after PM return is valid transport evidence,
                # not proof that its result was incorporated in the PM assessment.
                assessment["observerDrainedAfterPM"] = waiting is not None
                if waiting is not None:
                    print("WARNING: PM returned before all work completed; review late worker results separately.", flush=True)
                evidence["acceptance"] = assessment
                (self.root / "evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
                print("Verified real worker receipts:", json.dumps(assessment["completedRealWorkerReceipts"]), flush=True)
                if not assessment["exactReceiptCitation"]:
                    print("WARNING: PM prose does not reproduce an exact observed worker receipt ID; manual quality review advised.", flush=True)
                else:
                    print("Quality advisory: exact worker receipt cited.", flush=True)
                print("PASS: real PM, completed real worker and persisted review state; manual evidence review still required.", flush=True)
                if not self.args.summary_only and not self.args.smoke:
                    print(json.dumps(parent.get("result"), indent=2), flush=True)
                return
            time.sleep(.5)


def terminal_diagnostic(delivery):
    result = delivery.get("result")
    status = result.get("status", {}) if isinstance(result, dict) else {}
    message = status.get("message", {}) if isinstance(status, dict) else {}
    parts = message.get("parts", []) if isinstance(message, dict) else []
    known = {"ACP prompt timed out", "ACP execution failed", "ACP did not complete the turn",
             "Gateway busy; retry after the active turn finishes"}
    reasons = [part.get("text") for part in parts if isinstance(part, dict)
               and isinstance(part.get("text"), str) and part["text"] in known]
    metadata = result.get("metadata", {}) if isinstance(result, dict) else {}
    if not isinstance(metadata, dict):
        metadata = {}
    stop = metadata.get("opencode.stopReason")
    allowed_stops = {"end_turn", "max_tokens", "max_turn_requests", "refusal", "cancelled"}
    return {"state": delivery["state"], "gatewayReason": reasons[0] if reasons else "not exposed",
            "stopReason": stop if isinstance(stop, str) and stop in allowed_stops else "not exposed",
            "permissionDenied": metadata.get("opencode.permissionDenied") == "true",
            "hasResult": bool(result)}


def verify_evidence(e):
    real = {a["id"] for a in e["agents"] if a["runtime"] == "opencode"}
    if e["root"]["target"] != "pm" or e["root"]["state"] != "completed" or "pm" not in real or not e["root"].get("result"):
        raise RuntimeError("PM did not produce a completed live result")
    root_id = e["root"]["id"]
    worker = [d for d in e["deliveries"] if d["id"] != root_id and d["state"] == "completed"
              and (d.get("assignedAgent") or d["target"]) in real - {"pm"} and d.get("result")]
    if not worker:
        raise RuntimeError("no completed independent real worker result")
    if any(d["state"] != "completed" for d in e["deliveries"]):
        raise RuntimeError("unresolved delivery; inspect evidence instead of claiming success")
    if not any(s["key"] == "pm" and s.get("revision", 0) > 0 and s.get("value") for s in e["state"]):
        raise RuntimeError("missing PM shared-state evidence")
    workers = {d.get("assignedAgent") or d["target"] for d in worker}
    if not any(s["key"] in workers and s.get("revision", 0) > 0 and s.get("value") for s in e["state"]):
        raise RuntimeError("missing real worker shared-state evidence")

    # Infrastructure acceptance is based on the authoritative delivery and
    # KV records from the isolated run, not a model copying a long opaque ID.
    # Exact textual citation is retained as an advisory quality signal.
    result_text = json.dumps(e["root"]["result"])
    return {
        "completedRealWorkerReceipts": sorted({d["id"] for d in worker}),
        "exactReceiptCitation": any(d["id"] in result_text for d in worker),
    }


def valid_prompt_timeout(value):
    if value == "unlimited":
        return value
    match = re.fullmatch(r"([0-9]{1,10})(ms|s|m|h)", value)
    if match:
        ms = int(match.group(1)) * {"ms": 1, "s": 1000, "m": 60000, "h": 3600000}[match.group(2)]
        if 30 <= ms <= 2147483647:
            return value
    raise argparse.ArgumentTypeError("prompt timeout must be 30ms–2147483647ms, or unlimited")


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--team", default=str(HERE / "team.json"))
    p.add_argument("--profile", choices=["configured", "simulated", "real"], default="configured")
    p.add_argument("--gateway", default="opencode-a2a-lite")
    p.add_argument("--prompt-timeout", type=valid_prompt_timeout, default="60m", help="gateway ACP turn deadline, independent of team wall-clock budget")
    p.add_argument("--model", help="optional OpenCode provider/model; omit for credential-free default")
    p.add_argument("--pass-env", action="append", default=[], help="explicit provider variable; repeat")
    p.add_argument("--port", type=int, default=19080)
    p.add_argument("--seconds", type=int, default=600, help="runtime deadline including startup (60–1800s)")
    p.add_argument("--headless", action="store_true")
    p.add_argument("--summary-only", action="store_true", help="do not print raw model output; retain private evidence")
    p.add_argument("--smoke", action="store_true", help="minimal credential-free live architecture consultation")
    p.add_argument("--check", action="store_true", help="provision only; never submit a model task")
    p.add_argument("--objective", default="Review the fictional controller fixture for architectural, implementation and verification risks. Collaborate with the team and produce a consolidated evidence-based assessment.")
    p.add_argument("--accept-live-cost", action="store_true", help="allow model execution with explicit provider credentials")
    p.add_argument("--accept-free-model", action="store_true", help="allow credential-free live inference on the synthetic fixture")
    args = p.parse_args()
    team = load_team(args.team, args.profile)
    real = [a for a in team["agents"] if a["runtime"] == "opencode"]
    if not 1024 <= args.port <= 65535 - len(team["agents"]) or not 60 <= args.seconds <= 1800:
        p.error("invalid port or deadline")
    if args.smoke:
        if args.profile != "configured" or args.pass_env or {a["id"] for a in real} != {"pm", "architect"}:
            p.error("smoke requires the default hybrid team and no provider credentials")
        args.objective = "Review the fictional controller for automatic-restart risk. Discover and consult one real architecture worker, collect its finding and save a short receipt-linked review note. Return a concise conclusion citing the completed delivery ID. Do not delegate any other work."
    if len(args.objective.encode()) > 4096 or not args.objective.strip():
        p.error("objective must be 1–4096 bytes")
    if real:
        if sys.platform != "linux" or (args.model and not re.fullmatch(r"[^/\s]+/[^/\s]+", args.model)):
            p.error("real agents require Linux and valid optional provider/model")
        if args.pass_env:
            if not args.model or any(not re.fullmatch(r"[A-Z][A-Z0-9_]*_API_KEY", k) or not os.environ.get(k) for k in args.pass_env):
                p.error("credentialed mode requires a model and explicit nonempty provider *_API_KEY variable(s)")
            if args.accept_free_model:
                p.error("free-model mode never forwards credentials")
        elif args.model and not (args.model == "opencode/big-pickle" or
                                 re.fullmatch(r"opencode/[a-z0-9][a-z0-9.-]*-free", args.model)):
            p.error("credential-free mode requires OpenCode default, opencode/big-pickle or an opencode/*-free model")
        args.gateway = shutil.which(args.gateway)
        if not args.gateway or subprocess.check_output([args.gateway, "--version"], env=clean_environment(), timeout=15, text=True).strip() != "0.8.2":
            p.error("install pinned opencode-a2a-lite 0.8.2")
        opencode_version = subprocess.check_output(["opencode", "--version"], env=clean_environment(), timeout=15, text=True).strip()
        if not opencode_supported(opencode_version):
            p.error("install stable OpenCode 2.3.2 or newer")
    if not args.check:
        if len(real) < 2 or not any(a["id"] == "pm" for a in real):
            p.error("live run requires a real PM and at least one real worker; use --check for provider-free setup")
        if args.pass_env and not args.accept_live_cost:
            p.error("credentialed live run requires --accept-live-cost")
        if not args.pass_env and (not args.accept_free_model or args.accept_live_cost):
            p.error("credential-free live run requires --accept-free-model, not --accept-live-cost")
    # Private standalone root outside the checkout. Retain artifacts for
    # operator review; never automatically publish model content or logs.
    os.umask(0o077)
    root = Path(tempfile.mkdtemp(prefix="agentiplex-autonomous-", dir="/tmp")).resolve()
    print("Private run directory:", root, flush=True)
    if real:
        print("Requested model:", args.model or "OpenCode default",
              "| credentials:", "explicit provider" if args.pass_env else "none",
              "| gateway prompt timeout:", args.prompt_timeout, "| team deadline seconds:", args.seconds, flush=True)
    build_env = {k: v for k, v in os.environ.items() if not k.endswith("_API_KEY") and not k.startswith("AGENTIPLEX_")}
    if not args.headless:
        subprocess.run(["go", "generate", "./internal/dashboard"], env=build_env, cwd=ROOT, check=True, timeout=300)
    for name, pkg in (("agentiplexd", "./cmd/agentiplexd"), ("agentiplex", "./cmd/agentiplex"),
                      ("simulated-agent", "./examples/simulated-agent")):
        subprocess.run(["go", "build", "-mod=readonly", "-o", str(root / name), pkg], cwd=ROOT, env=build_env, check=True, timeout=300)
    # Reject pre-existing listeners before launch. The bus uses a fixed port;
    # simulators use distinct ports, real gateways announce ephemeral ports.
    for port in range(args.port, args.port + len(team["agents"]) + 1):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", port))
    runner = Team(args, team, root, time.monotonic() + args.seconds)
    try:
        runner.start()
        if args.check:
            print("PASS: provisioned team only; no model collaboration claimed.", flush=True)
        else:
            runner.observe(args.objective)
    finally:
        runner.stop()
        print("Owned processes stopped. Review/delete private run directory:", root, flush=True)


if __name__ == "__main__":
    def terminate(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, terminate)
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
    except Exception as error:
        print("BLOCKED:", error, file=sys.stderr)
        sys.exit(1)
