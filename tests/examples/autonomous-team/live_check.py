#!/usr/bin/env python3
"""Live acceptance of examples/autonomous-team: real pm + architect gateways, simulated workers.

Starts exactly the processes the example README documents (ports and roster come from team.yaml), submits
the one objective, then independently verifies the daemon's delivery and state records. Test-only; never
used for normal operation. Uses OpenCode's default model unless --model is given (free models only here).
"""
import argparse
import json
import re
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import procs

ROOT = Path(__file__).resolve().parents[3]
EX = ROOT / "examples" / "autonomous-team"
TERMINAL = {"completed", "failed", "partial", "uncertain", "input-required", "auth-required"}
REAL = {"pm": "planning", "architect": "architecture"}
SIMULATED = {"developer": "development", "tester-1": "testing", "tester-2": "testing", "quality": "quality"}
OBJECTIVE = ("Review the fictional controller fixture for architectural, implementation and verification risks. "
             "Collaborate with the team and produce a consolidated evidence-based assessment.")


def verify_evidence(root, deliveries, state):
    """Acceptance rests on delivery and state records, not on the model quoting an ID."""
    if root["target"] != "pm" or root["state"] != "completed" or not root.get("result"):
        raise RuntimeError("PM did not produce a completed live result")
    if any(d["state"] != "completed" for d in deliveries):
        raise RuntimeError("unresolved delivery; inspect evidence instead of claiming success")
    workers = {d.get("assignedAgent") or d["target"] for d in deliveries
               if d["id"] != root["id"] and d.get("result")} & {"architect"}
    if not workers:
        raise RuntimeError("no completed independent real worker result")
    if not any(s["key"] == "pm" and s.get("revision", 0) > 0 and s.get("value") for s in state):
        raise RuntimeError("missing PM shared-state evidence")
    if not any(s.get("revision", 0) > 0 and s.get("value") and (s["key"] in workers or s["key"].startswith("note-architect-"))
               for s in state):
        raise RuntimeError("missing real worker shared-state evidence")
    return sorted(d["id"] for d in deliveries if d["id"] != root["id"])


def ports(team):
    found = dict(re.findall(r'(\S+): \{url: "http://127\.0\.0\.1:(\d+)"\}', team.read_text()))
    return {k: int(v) for k, v in found.items()}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--model", help="provider/model (omit for the OpenCode default)")
    p.add_argument("--provision-only", action="store_true", help="start and register the team, submit nothing")
    p.add_argument("--seconds", type=int, default=900)
    p.add_argument("--prompt-timeout", default="10m")
    p.add_argument("--startup-retries", type=int, default=4, help="the gateway preflight is known to be flaky")
    args = p.parse_args()
    run = Path(tempfile.mkdtemp(prefix="agentiplex-autonomous-check-"))
    print("Private run directory:", run, flush=True)
    team = EX / "team.yaml"
    port = ports(team)
    team_text = team.read_text()
    (run / "team.yaml").write_text(team_text.replace("/tmp/agentiplex-autonomous/", str(run) + "/"))
    for name in ("agentiplexd", "agentiplex", "simulated-agent"):
        pkg = "./examples/simulated-agent" if name == "simulated-agent" else "./cmd/" + name
        subprocess.run(["go", "build", "-mod=readonly", "-o", str(run / name), pkg], cwd=ROOT, check=True, timeout=300)
    procs.private_policies(EX / "policies", run)
    owned = procs.Procs(str(run))
    api = "http://127.0.0.1:%d" % int(re.search(r'listen: "127\.0\.0\.1:(\d+)"', team_text).group(1))
    cli = lambda *a: json.loads(subprocess.check_output([str(run / "agentiplex"), "--server", api, *a], env=procs.clean_env(), timeout=30))
    try:
        for name, skill in SIMULATED.items():
            owned.spawn(name, [str(run / "simulated-agent"), "--name", name, "--skill", skill, "--port", str(port[name])])
        for name in REAL:
            ws = run / name
            shutil.copytree(EX / "profiles" / name, ws)
            shutil.copytree(EX / "fixture", ws / "fixture")
            if args.model:
                cfg = json.loads((ws / "opencode.json").read_text())
                provider, model = args.model.split("/", 1)
                cfg["model"] = {"providerID": provider, "model": model}
                (ws / "opencode.json").write_text(json.dumps(cfg))
            policy = run / "policies" / ("coordinator.json" if name == "pm" else "worker.json")
            env = procs.clean_env(A2A_NAME=name, A2A_DESCRIPTION="Read-only engineering " + REAL[name], A2A_TAGS=REAL[name] + ",real-opencode")
            for attempt in range(args.startup_retries + 1):
                proc = owned.spawn(name, [*procs.gateway_command(), "--workspace", str(ws), "--port", str(port[name]), "--access", "read",
                                         "--policy-file", str(policy), "--prompt-timeout", args.prompt_timeout], env, ws)
                try:
                    owned.wait_url(name, proc, "http://127.0.0.1:%d/.well-known/agent-card.json" % port[name], 600)
                    break
                except RuntimeError:
                    if attempt == args.startup_retries:
                        raise
                    print(name, "startup failed (known flaky preflight); retrying", flush=True)
                    time.sleep(2)
        daemon = owned.spawn("daemon", [str(run / "agentiplexd"), "--config", str(run / "team.yaml")])
        owned.wait_url("daemon", daemon, api + "/readyz", 120)
        listed = {a["id"] for a in cli("agents", "list")}
        if listed != set(port):
            raise RuntimeError("registry mismatch: %s" % sorted(listed))
        print("Team ready:", sorted(listed), flush=True)
        if args.provision_only:
            print("PASS: provisioned team only; no model collaboration claimed.")
            return
        root_id = cli("send", "pm", OBJECTIVE, "--idempotency-key", "autonomous-review")["id"]
        end = time.monotonic() + args.seconds
        while True:
            if time.monotonic() > end:
                raise TimeoutError("team deadline exceeded")
            if daemon.poll() is not None:
                raise RuntimeError("daemon exited")
            records = procs.get(api + "/api/v1/deliveries?limit=100")
            if any(d["id"] == root_id and d["state"] in TERMINAL for d in records) and all(d["state"] in TERMINAL for d in records):
                break
            time.sleep(1)
        deliveries = [cli("messages", "status", d["id"]) for d in records]
        root = next(d for d in deliveries if d["id"] == root_id)
        state = [cli("state", "get", "review", e["key"]) for e in cli("state", "list", "review")]
        (run / "evidence.json").write_text(json.dumps({"root": root, "deliveries": deliveries, "state": state}, indent=2))
        print("Verified worker deliveries:", json.dumps(verify_evidence(root, deliveries, state)), flush=True)
        print("PASS: real PM, completed real worker and persisted review state; manual evidence review still required.")
    finally:
        owned.stop()
        print("Owned processes stopped. Review/delete:", run, flush=True)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, TimeoutError, subprocess.SubprocessError) as exc:
        print("BLOCKED:", exc, file=sys.stderr)
        sys.exit(1)
