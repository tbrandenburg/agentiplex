#!/usr/bin/env python3
"""Startup-only check of examples/repository-team: five real gateways (static policies) on a synthetic Git
workspace, then the daemon with team.yaml. Submits no model task. Test-only.
"""
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
EX = ROOT / "examples" / "repository-team"
POLICY = {"pm": ("coordinator", "read"), "architect": ("reader", "read"), "developer": ("writer", "danger-full-access"),
          "tester": ("writer", "danger-full-access"), "quality": ("reader", "read")}
RETRIES = 4  # the gateway preflight is known to be flaky


def git(ws, *a):
    return subprocess.run(["git", "-C", str(ws), *a], check=True, capture_output=True, text=True).stdout


def main():
    run = Path(tempfile.mkdtemp(prefix="agentiplex-repository-check-"))
    print("Private run directory:", run, flush=True)
    ws = run / "workspace"
    ws.mkdir()
    git(ws, "init", "-q")
    git(ws, "config", "user.name", "Example CI")
    git(ws, "config", "user.email", "ci@example.invalid")
    (ws / "README.md").write_text("Synthetic repository\n")
    shutil.copy(EX / "opencode.json", ws / "opencode.json")
    git(ws, "add", ".")
    git(ws, "commit", "-qm", "synthetic fixture")
    team = (EX / "team.yaml").read_text()
    (run / "team.yaml").write_text(team.replace("/tmp/agentiplex-repository/", str(run) + "/"))
    ports = {k: int(v) for k, v in re.findall(r'(\S+): \{url: "http://127\.0\.0\.1:(\d+)"\}', team)}
    api = "http://127.0.0.1:%d" % int(re.search(r'listen: "127\.0\.0\.1:(\d+)"', team).group(1))
    for name in ("agentiplexd", "agentiplex"):
        subprocess.run(["go", "build", "-mod=readonly", "-o", str(run / name), "./cmd/" + name], cwd=ROOT, check=True, timeout=300)
    procs.private_policies(EX / "policies", run)
    owned = procs.Procs(str(run))
    try:
        for name, (policy, access) in POLICY.items():
            env = procs.clean_env(A2A_NAME=name, A2A_DESCRIPTION=name + " of the repository team", A2A_TAGS=name + ",real-opencode")
            for attempt in range(RETRIES + 1):
                proc = owned.spawn(name, [*procs.gateway_command(), "--workspace", str(ws), "--port", str(ports[name]), "--access", access,
                                         "--policy-file", str(run / "policies" / (policy + ".json"))], env, ws)
                try:
                    owned.wait_url(name, proc, "http://127.0.0.1:%d/.well-known/agent-card.json" % ports[name], 600)
                    break
                except RuntimeError:
                    if attempt == RETRIES:
                        raise
                    print(name, "startup failed (known flaky preflight); retrying", flush=True)
                    time.sleep(2)
        daemon = owned.spawn("daemon", [str(run / "agentiplexd"), "--config", str(run / "team.yaml")])
        owned.wait_url("daemon", daemon, api + "/readyz", 120)
        agents = json.loads(subprocess.check_output([str(run / "agentiplex"), "--server", api, "agents", "list"], timeout=30))
        if {a["id"] for a in agents} != set(ports):
            raise RuntimeError("registry mismatch")
        if git(ws, "status", "--porcelain"):
            raise RuntimeError("provisioning changed the workspace")
        print("PASS: real gateways provisioned and registered; no model task submitted.")
    finally:
        owned.stop()
        print("Owned processes stopped. Review/delete:", run, flush=True)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, TimeoutError, subprocess.SubprocessError) as exc:
        print("BLOCKED:", exc, file=sys.stderr)
        sys.exit(1)
