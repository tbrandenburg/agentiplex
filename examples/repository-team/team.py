#!/usr/bin/env python3
"""Operator-owned shared repository example; no workspace handling in Agentiplex."""
import argparse
import importlib.util
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

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
# Reuse the existing example's process, readiness and CLI helpers, not its
# fixture generator, role configuration, or fixture-specific acceptance.
spec = importlib.util.spec_from_file_location("autonomous_team", HERE.parent / "autonomous-team" / "team.py")
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)

ROLES = {
    "pm": "Coordinate the implementation. Delegate concrete tasks to the advertised roles.",
    "architect": "Analyze the current repository and recommend a bounded implementation.",
    "developer": "Implement the agreed change in the shared workspace, run relevant builds/tests and fix failures.",
    "tester": "Independently run repository build/test/lint commands, report commands, actual outcomes and exit codes; avoid editing project source.",
    "quality": "Independently review the actual changes and identify risks or blockers.",
}
NAMESPACE = "repository"
FULL_ACCESS_ROLES = {"developer", "tester"}
FULL_ACCESS_WARNING = (
    "WARNING: Developer and both Testers have FULL ACCESS: shell commands run without "
    "approval as your OS user, can read or change files outside the workspace, use the "
    "network, access forwarded API keys, modify Git and bypass MCP role grants. "
    "There is NO OS sandbox or rollback. Any local client reaching their ports can invoke them. "
    "Use only a disposable, isolated VM/container/user with minimal credentials and a trusted checkout."
)


def git(workspace, *args):
    return subprocess.check_output(
        ["git", "-C", str(workspace), "-c", "core.fsmonitor=false", *args],
        timeout=30, env=base.clean_environment(),
    )


def git_snapshot(workspace):
    return {
        "head": git(workspace, "rev-parse", "HEAD").decode().strip(),
        "branch": git(workspace, "rev-parse", "--abbrev-ref", "HEAD").decode().strip(),
        "status": git(workspace, "status", "--porcelain=v1", "--untracked-files=all"),
        "diff": git(workspace, "diff", "--no-ext-diff", "--no-textconv", "--binary", "HEAD", "--"),
        "index": git(workspace, "diff", "--cached", "--no-ext-diff", "--no-textconv", "HEAD", "--"),
    }


def inspect_workspace(path, endpoint):
    if not path:
        raise ValueError("supply an existing trusted Git workspace with --workspace")
    workspace = Path(path).expanduser().resolve(strict=True)
    if not workspace.is_dir() or workspace != Path(git(workspace, "rev-parse", "--show-toplevel").decode().strip()).resolve():
        raise ValueError("workspace must be the root of an existing Git checkout/worktree")
    before = git_snapshot(workspace)
    if before["status"] or before["diff"] or before["index"]:
        raise ValueError("workspace must be clean; prepare a separate branch/worktree yourself")
    # The operator owns the common project config. Never rewrite it or inject
    # per-agent files into the shared repository.
    config = json.loads((workspace / "opencode.json").read_text(encoding="utf-8"))
    if not isinstance(config, dict) or set(config) - {"$schema", "model", "mcp"}:
        raise ValueError("use the minimal reviewed opencode.json described in the example README")
    model = config.get("model")
    if not isinstance(model, dict) or set(model) != {"providerID", "model"} or any(
        not isinstance(v, str) or not re.fullmatch(r"[A-Za-z0-9._:-]+", v) for v in model.values()
    ):
        raise ValueError("opencode.json must select an explicit providerID and model")
    expected = {"servers": {"agentiplex": {
        "type": "remote", "url": endpoint + "/mcp", "protocol": "auto", "codemode": False,
    }}}
    if config.get("mcp") != expected:
        raise ValueError("opencode.json must contain only the exact Agentiplex MCP server for this port")
    return workspace, before, model


def policy_for(agent):
    profile = "full-access" if agent["role"] in FULL_ACCESS_ROLES else "read-only"
    tools = base.READ_TOOLS + (base.DELEGATE_TOOLS if agent["id"] == "pm" else ())
    return profile, {"version": 1, "base": profile, "policies": [
        {"action": "mcp.use", "resource": "agentiplex", "effect": "allow"},
        *[{"action": "tool.use", "resource": "agentiplex_" + t + ":*", "effect": "allow"} for t in tools],
    ]}


def gateway_command(args, workspace, policy_path, profile):
    command = [args.gateway, "--workspace", str(workspace), "--port", "0",
               "--policy", profile, "--permissions", "reject",
               "--policy-file", str(policy_path), "--prompt-timeout", args.prompt_timeout]
    if profile == "full-access":
        if not args.accept_full_access:
            raise ValueError("full access requires --accept-full-access")
        command.append("--dangerously-skip-permissions")
    for key in args.pass_env:
        command += ["--pass-env", key]
    return command


def pm_prompt(objective):
    roles = "\n".join(role + ": " + instruction for role, instruction in ROLES.items())
    prompt = (
        "You are the PM of a real repository team. All six registered agents have independent "
        "sessions in the SAME operator-selected Git workspace. Developer and testers have full shell "
        "and edit capability; only developer should intentionally edit source. Discover actual agents, "
        "consult architect, delegate implementation and local tests to developer, wait for completion, "
        "dispatch independent executable verification to one tester, and ask quality for review. "
        "If verification fails, delegate a fix and retest before quality review within the budget. "
        "Do not run concurrent edits or tests against changing code. Choose detailed tasks yourself; "
        "tell each recipient its role and objective, with relevant evidence. "
        "Use distinct idempotency keys, poll actual receipts with bounded backoff, and do not "
        "resubmit ambiguous work. No more than six delegations; no delegation back to PM. "
        "Use shared state namespace repository; ask developer to save a finding under key developer, "
        "ask tester to save its verification report under its actual agent ID (tester-1 or tester-2), "
        "and save your summary under key pm. Read existing revisions before updating. "
        "Require tester to report actual commands, exit codes, relevant output, and any failure or NOT RUN. "
        "Do not invent files, changes, tests or receipts. Instruct workers not to commit, stage, push, "
        "merge, change branches, access credentials or files outside the project. These are role instructions, "
        "not enforced restrictions on full-access agents. Leave Git publication to the operator. "
        "Return only after all delegated work is completed or report a blocker honestly.\n"
        + roles + "\nOperator objective:\n" + objective
    )
    if len(prompt.encode()) > 4096:
        raise ValueError("objective too long for the bounded A2A submission")
    return prompt


def verify_collaboration(evidence):
    root = evidence["root"]
    if root["target"] != "pm" or root["state"] != "completed" or not root.get("result"):
        raise RuntimeError("PM did not return a completed result")
    records = evidence["deliveries"]
    if any(d["state"] != "completed" for d in records):
        raise RuntimeError("a delegated delivery did not complete")
    workers = {}
    for d in records:
        if d["id"] != root["id"] and d.get("result"):
            worker = d.get("assignedAgent") or d["target"]
            workers.setdefault(worker, []).append(d["id"])
    if not {"architect", "developer", "quality"} <= workers.keys() or not {"tester-1", "tester-2"} & workers.keys():
        raise RuntimeError("missing completed architect/developer/tester/quality receipts")
    keys = {s["key"] for s in evidence["state"] if s.get("revision", 0) > 0 and s.get("value")}
    if not {"pm", "developer"} <= keys:
        raise RuntimeError("missing PM/developer shared-state evidence")
    if not ({"tester-1", "tester-2"} & workers.keys() & keys):
        raise RuntimeError("missing completed tester shared-state verification report")
    return workers


class RepositoryTeam(base.Team):
    def start(self):
        if not self.args.accept_full_access:
            raise ValueError("full access requires --accept-full-access")
        self.spawn("daemon", [str(self.root / "agentiplexd"), "--listen", "127.0.0.1:" + str(self.args.port),
                   "--public-url", self.endpoint, "--broker-mode", "embedded", "--namespace", "repository-demo",
                   "--storage", str(self.root / "jetstream"), "--mcp",
                   "--dashboard=" + str(not self.args.headless).lower()],
                   dict(self.env, AGENTIPLEX_DELIVERY_TIMEOUT="30s",
                        AGENTIPLEX_DELIVERY_RETRY_DELAY="2s", AGENTIPLEX_DELIVERY_MAX_ATTEMPTS="80"))
        while True:
            self.alive()
            try:
                if base.get(self.endpoint + "/readyz").get("ready"):
                    break
            except (OSError, ValueError):
                pass
            time.sleep(.2)
        for agent in self.team["agents"]:
            name, skill = agent["id"], agent["skill"]
            profile, policy = policy_for(agent)
            path = self.root / (name + "-policy.json")
            path.write_text(json.dumps(policy, indent=2) + "\n")
            env = dict(self.env, A2A_NAME=name, A2A_DESCRIPTION=ROLES[agent["role"]],
                       A2A_TAGS=skill + ",real-opencode,repository-team")
            for key in self.args.pass_env:
                env[key] = os.environ[key]
            proc = self.spawn(name, gateway_command(self.args, self.args.workspace, path, profile),
                              env, self.args.workspace)
            url = self.gateway_ready(name, proc)
            card = self.wait_card(url, proc, name, skill)
            tags = {t for s in card.get("skills", []) for t in s.get("tags", [])}
            if "simulated" in tags or "real-opencode" not in tags:
                raise RuntimeError("unexpected agent runtime label")
            self.cli("register", name, url)
            print("Registered", name, profile, url, flush=True)
        for kind in ("groups", "pools"):
            for name, members in self.team[kind].items():
                self.cli(kind, "set", name, *members)
        if {a["id"] for a in self.cli("agents", "list")} != {a["id"] for a in self.team["agents"]}:
            raise RuntimeError("registry mismatch")
        if base.get(self.endpoint + "/api/v1/deliveries?limit=100"):
            raise RuntimeError("unexpected existing deliveries")
        print("Team ready:", self.endpoint + "/ui/", flush=True)

    def observe_repository(self, objective):
        root_id = self.cli("send", "pm", pm_prompt(objective), "--idempotency-key", "repository-objective")["id"]
        previous = None
        while True:
            self.alive()
            records = self.snapshot(root_id)
            summary = sorted((d["id"], d["target"], d["state"]) for d in records)
            if summary != previous:
                print("Observed:", json.dumps(summary), flush=True)
                previous = summary
            parent = self.cli("messages", "status", root_id)
            if parent["state"] in base.TERMINAL:
                # Refresh after observing PM completion so a final delegation
                # admitted between the earlier snapshot and completion is included.
                records = self.snapshot(root_id)
                details = {d["id"]: self.cli("messages", "status", d["id"]) for d in records}
                details[root_id] = parent
                for d in list(details.values()):
                    for child in d.get("children") or []:
                        details[child["id"]] = self.cli("messages", "status", child["id"])
                state = [self.cli("state", "get", NAMESPACE, s["key"])
                         for s in self.cli("state", "list", NAMESPACE)]
                evidence = {"root": parent, "deliveries": list(details.values()), "state": state,
                            "agents": self.team["agents"], "testExecution": "agent-reported; not independently attested"}
                (self.root / "evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
                # Drain outstanding work within the existing deadline, not just until PM returns.
                if parent["state"] != "completed" or all(d["state"] in base.TERMINAL for d in details.values()):
                    receipts = verify_collaboration(evidence)
                    print("Completed worker receipts:", json.dumps(receipts), flush=True)
                    return
            time.sleep(.5)


def save_workspace_evidence(workspace, before, root, model):
    after = git_snapshot(workspace)
    (root / "git-status.txt").write_bytes(after["status"])
    (root / "changes.diff").write_bytes(after["diff"])
    metadata = {"workspace": str(workspace), "initialHead": before["head"], "finalHead": after["head"],
                "initialBranch": before["branch"], "finalBranch": after["branch"], "requestedModel": model,
                "trackedDiff": bool(after["diff"]), "statusChanged": bool(after["status"]),
                "untrackedContentsIncluded": False}
    (root / "workspace.json").write_text(json.dumps(metadata, indent=2) + "\n")
    if after["head"] != before["head"] or after["branch"] != before["branch"] or after["index"]:
        raise RuntimeError("unexpected Git HEAD, branch or index change; inspect the workspace")
    return metadata


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--workspace", required=True)
    p.add_argument("--objective", default="")
    p.add_argument("--port", type=int, default=19080)
    p.add_argument("--seconds", type=int, default=1200)
    p.add_argument("--prompt-timeout", type=base.valid_prompt_timeout, default="10m")
    p.add_argument("--gateway", default="opencode-a2a-lite")
    p.add_argument("--pass-env", action="append", default=[])
    p.add_argument("--accept-full-access", action="store_true",
                   help="acknowledge unrestricted shell and host-user access for Developer and Testers")
    p.add_argument("--accept-provider-data", action="store_true", help="approve sending workspace data to the selected provider")
    p.add_argument("--public-workspace", action="store_true", help="attest that credential-free input is safe to disclose")
    p.add_argument("--check", action="store_true", help="provision only, never submit a model task")
    p.add_argument("--headless", action="store_true")
    args = p.parse_args()
    if not args.accept_full_access:
        p.error(FULL_ACCESS_WARNING + " Explicit --accept-full-access is required, including for --check.")
    print(FULL_ACCESS_WARNING, file=sys.stderr, flush=True)
    if sys.platform != "linux" or not 1024 <= args.port <= 65535 or not 60 <= args.seconds <= 1800:
        p.error("requires Linux, an unprivileged port and a 60–1800 second deadline")
    if args.pass_env and (len(set(args.pass_env)) != len(args.pass_env) or any(
        not re.fullmatch(r"[A-Z][A-Z0-9_]*_API_KEY", k) or not os.environ.get(k) for k in args.pass_env
    )):
        p.error("use explicit nonempty provider *_API_KEY variable names")
    if not args.check:
        if not args.objective.strip() or not args.accept_provider_data:
            p.error("live run requires an objective and explicit --accept-provider-data")
        if not args.pass_env and not args.public_workspace:
            p.error("credential-free runs require --public-workspace; never send private source to free models")
        pm_prompt(args.objective)
    args.workspace, before, model = inspect_workspace(args.workspace, "http://127.0.0.1:" + str(args.port))
    if not args.check and not args.pass_env and not (
        model["providerID"] == "opencode" and
        (model["model"] == "big-pickle" or model["model"].endswith("-free"))
    ):
        p.error("credential-free runs require an explicitly selected OpenCode free model")
    args.gateway = shutil.which(args.gateway)
    if not args.gateway or subprocess.check_output([args.gateway, "--version"], env=base.clean_environment(), timeout=15, text=True).strip() != "0.8.2":
        p.error("install pinned opencode-a2a-lite 0.8.2")
    if subprocess.check_output(["opencode", "--version"], env=base.clean_environment(), timeout=15, text=True).strip() != "opencode v2.3.2":
        p.error("install tested OpenCode 2.3.2")
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", args.port))
    os.umask(0o077)
    root = Path(tempfile.mkdtemp(prefix="agentiplex-repository-", dir="/tmp")).resolve()
    if root == args.workspace or args.workspace in root.parents:
        root.rmdir()
        p.error("private run directory must be outside the selected workspace")
    print("Private run directory:", root, flush=True)
    for name in ("agentiplexd", "agentiplex"):
        shutil.copy2(ROOT / "bin" / name, root / name)
    team = base.load_team(base.HERE / "team.json", "real")
    runner = RepositoryTeam(args, team, root, time.monotonic() + args.seconds)
    try:
        runner.start()
        if not args.check:
            runner.observe_repository(args.objective)
    finally:
        runner.stop()
        metadata = save_workspace_evidence(args.workspace, before, root, model)
        print("Owned processes stopped. Inspect private evidence at:", root, flush=True)
    if args.check:
        if metadata["statusChanged"]:
            raise RuntimeError("provisioning unexpectedly changed the workspace")
        print("PASS: six real gateways provisioned; no model task submitted.")
    elif not metadata["statusChanged"]:
        raise RuntimeError("collaboration completed but no Git-visible changes were produced")
    else:
        print("PASS: completed collaboration and Git-visible changes; agent-reported test results require human review or independent verification.")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
    except Exception as exc:
        print("BLOCKED:", exc, file=sys.stderr)
        sys.exit(1)
