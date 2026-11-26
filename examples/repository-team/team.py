#!/usr/bin/env python3
"""Operator-owned shared repository example; no workspace handling in Agentiplex."""
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

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))
sys.path.insert(0, str(HERE))
import teamlib
from teamlib import ID, READ_TOOLS, DELEGATE_TOOLS, TERMINAL, call_evidence, policy
from repo import (build_binaries, clone_workspace, directory_lock, git_snapshot, inspect_workspace,
                  model_selection, prepare_clone_config, save_workspace_evidence, source_url)

NAMESPACE = "repository"
ACCESS = {"read-only": "read", "full-access": "danger-full-access"}
FULL_ACCESS_WARNING = (
    "WARNING: Agents with full-access (in team.json) run shell commands without "
    "approval as your OS user, can read or change files outside the workspace, use the "
    "network, access forwarded API keys, modify Git and bypass MCP tool grants. "
    "There is NO OS sandbox or rollback. Any local client reaching their ports can invoke them. "
    "Use only a disposable, isolated VM/container/user with minimal credentials and a trusted checkout."
)


def load_roster(path):
    """The team is data: a list of {id, access, role}; the first agent coordinates."""
    agents = json.loads(Path(path).read_text())["agents"]
    if not isinstance(agents, list) or not 2 <= len(agents) <= 12:
        raise ValueError("expected 2–12 agents")
    for a in agents:
        if set(a) != {"id", "access", "role"} or a["access"] not in ACCESS:
            raise ValueError("each agent needs exactly id, access (read-only|full-access) and role")
        if not isinstance(a["id"], str) or not ID.fullmatch(a["id"]) or not isinstance(a["role"], str) or not a["role"].strip():
            raise ValueError("invalid agent id or role")
    if len({a["id"] for a in agents}) != len(agents):
        raise ValueError("duplicate agent id")
    return agents


def family(agent_id):
    return re.sub(r"-\d+$", "", agent_id)


def policy_for(agents, agent):
    """Everyone gets the collaboration tools; only the coordinator (first agent) may delegate."""
    tools = READ_TOOLS + (DELEGATE_TOOLS if agent is agents[0] else ())
    return agent["access"], policy(agent["access"], tools)


def log_tail(path, lines=3, width=300):
    try:
        text = path.read_text(errors="replace").strip().splitlines()[-lines:]
    except OSError:
        return ""
    return "".join("\n  log: " + line[:width] for line in text if line.strip())


def gateway_command(args, workspace, policy_path, profile):
    if profile == "full-access" and not args.accept_full_access:
        raise ValueError("full access requires --accept-full-access")
    command = [args.gateway, "--workspace", str(workspace), "--port", "0", "--access", ACCESS[profile],
               "--policy-file", str(policy_path), "--prompt-timeout", args.prompt_timeout]
    for key in args.pass_env:
        command += ["--pass-env", key]
    return command


def pm_prompt(agents, objective):
    roles = "\n".join("- " + a["id"] + ": " + a["role"] for a in agents[1:])
    prompt = (
        "You are " + agents[0]["id"] + " of a team working in one shared Git workspace. Reach the objective "
        "by delegating to the team below; have the change implemented, independently tested and "
        "reviewed before you finish. Share findings with the note and read_notes tools, namespace "
        + NAMESPACE + ". Nobody commits, stages, pushes, merges, changes branches or touches credentials "
        "or files outside the project; leave Git publication to the operator. Never invent results; "
        "report failures and blockers honestly.\nTeam:\n" + roles + "\nObjective:\n" + objective)
    if len(prompt.encode()) > 4096:
        raise ValueError("objective too long for the bounded A2A submission")
    return prompt


def verify_collaboration(evidence):
    root, agents = evidence["root"], evidence["agents"]
    if root["target"] != agents[0]["id"] or root["state"] != "completed" or not root.get("result"):
        raise RuntimeError("PM did not return a completed result")
    records = evidence["deliveries"]
    if any(d["state"] != "completed" for d in records):
        raise RuntimeError("a delegated delivery did not complete")
    done = {family(d.get("assignedAgent") or d["target"]) for d in records if d["id"] != root["id"] and d.get("result")}
    missing = {family(a["id"]) for a in agents[1:]} - done
    if missing:
        raise RuntimeError("missing completed worker results: " + ", ".join(sorted(missing)))
    if evidence["toolCalls"]["askCalls"] < 1:
        raise RuntimeError("no evidence that the PM called ask (no delivery returned to an ask)")
    if not any(s["key"].startswith("note-") for s in evidence["state"]):
        raise RuntimeError("missing shared notes")
    return sorted(done)


class RepositoryTeam(teamlib.Team):
    def start(self):
        self.start_daemon("repository-demo", not self.args.headless)
        self.start_agents()

    def start_agents(self):
        agents = self.team["agents"]
        for agent in agents:
            name = agent["id"]
            profile, rules = policy_for(agents, agent)
            path = self.root / (name + "-policy.json")
            path.write_text(json.dumps(rules, indent=2) + "\n")
            env = dict(self.env, A2A_NAME=name, A2A_DESCRIPTION=agent["role"],
                       A2A_TAGS=name + ",real-opencode,repository-team")
            for key in self.args.pass_env:
                env[key] = os.environ[key]
            print("Starting", name, "gateway (runtime preflight can take a minute or more)", flush=True)
            proc = self.spawn(name, gateway_command(self.args, self.args.workspace, path, profile),
                              env, self.args.workspace)
            try:
                url = self.gateway_ready(name, proc)
                card = self.wait_card(url, proc, name, name)
            except RuntimeError as exc:
                raise RuntimeError(str(exc) + log_tail(self.root / (name + ".log"))) from exc
            tags = {t for s in card.get("skills", []) for t in s.get("tags", [])}
            if "simulated" in tags or "real-opencode" not in tags:
                raise RuntimeError("unexpected agent runtime label")
            self.cli("register", name, url)
            print("Registered", name, profile, url, flush=True)
        if {a["id"] for a in self.cli("agents", "list")} != {a["id"] for a in agents}:
            raise RuntimeError("registry mismatch")
        if teamlib.get(self.endpoint + "/api/v1/deliveries?limit=100"):
            raise RuntimeError("unexpected existing deliveries")
        print("Team ready:", self.endpoint + "/ui/", flush=True)

    def observe_repository(self, objective):
        agents = self.team["agents"]
        root_id = self.cli("send", agents[0]["id"], pm_prompt(agents, objective), "--idempotency-key", "repository-objective")["id"]
        previous, drained = None, False
        while True:
            self.alive()
            records = teamlib.get(self.endpoint + "/api/v1/deliveries?limit=100")
            summary = sorted((d["id"], d["target"], d["state"]) for d in records)
            if summary != previous:
                print("Observed:", json.dumps(summary), flush=True)
                previous = summary
            if self.cli("messages", "status", root_id)["state"] in TERMINAL:
                # Refresh after PM completion so a late delegation is included.
                parent, details = self.details(root_id, teamlib.get(self.endpoint + "/api/v1/deliveries?limit=100"))
                state = [self.cli("state", "get", NAMESPACE, s["key"]) for s in self.cli("state", "list", NAMESPACE)]
                evidence = {"root": parent, "deliveries": details, "state": state, "agents": agents,
                            "toolCalls": call_evidence(details, agents[0]["id"]),
                            "daemonRestarts": self.daemon_restarts,
                            "testExecution": "agent-reported; not independently attested"}
                (self.root / "evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
                print("PM tool-call evidence:", json.dumps(evidence["toolCalls"]), flush=True)
                # Drain outstanding work within the existing deadline, not just until PM returns.
                if parent["state"] == "completed" and not all(d["state"] in TERMINAL for d in details) and not drained:
                    print("WARNING: PM returned before all work finished; waiting for it. The PM result may not reflect late workers.", flush=True)
                    drained = True
                if parent["state"] != "completed" or all(d["state"] in TERMINAL for d in details):
                    print("Completed worker results:", json.dumps(verify_collaboration(evidence)), flush=True)
                    return
            time.sleep(.5)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    source = p.add_mutually_exclusive_group(required=True)
    source.add_argument("--workspace", help="existing clean Git checkout")
    source.add_argument("--repo", help="Git URL to clone into an independent scratch worktree")
    p.add_argument("--ref", help="branch or tag to clone (default: remote HEAD)")
    p.add_argument("--model", help="provider/model for a cloned workspace; omit to use the OpenCode default")
    p.add_argument("--objective", default="")
    p.add_argument("--port", type=int, help="default 0 (automatic) for --repo, 19080 for --workspace")
    p.add_argument("--team", default=str(HERE / "team.json"), help="agent roster (id, access, role); first agent coordinates")
    p.add_argument("--seconds", type=int, default=3600, help="launcher deadline incl. startup (60-86400); must exceed --prompt-timeout")
    p.add_argument("--prompt-timeout", type=teamlib.valid_prompt_timeout, default="45m",
                   help="gateway deadline for one whole PM turn (all asks summed)")
    p.add_argument("--mcp-timeout", type=teamlib.valid_prompt_timeout, default="45m",
                   help="OpenCode mcp.timeout.execution: longest single tool call, e.g. one ask")
    p.add_argument("--restart-daemon", action="store_true", help="restart the daemon on the same storage and port if it dies (needs --port)")
    p.add_argument("--gateway", default="opencode-a2a-lite")
    p.add_argument("--pass-env", action="append", default=[])
    p.add_argument("--accept-full-access", action="store_true",
                   help="acknowledge unrestricted shell and host-user access for full-access agents")
    p.add_argument("--accept-provider-data", action="store_true", help="approve sending workspace data to the selected provider")
    p.add_argument("--public-workspace", action="store_true", help="attest that credential-free input is safe to disclose")
    p.add_argument("--check", action="store_true", help="provision only, never submit a model task")
    p.add_argument("--headless", action="store_true")
    args = p.parse_args()
    if not args.accept_full_access:
        p.error(FULL_ACCESS_WARNING + " Explicit --accept-full-access is required, including for --check.")
    print(FULL_ACCESS_WARNING, file=sys.stderr, flush=True)
    if args.port is None:
        args.port = 0 if args.repo else 19080
    if sys.platform != "linux" or not (1024 <= args.port <= 65535 or args.repo and args.port == 0) or not 60 <= args.seconds <= 86400:
        p.error("requires Linux, an unprivileged port (or 0 for --repo), and a 60–86400 second deadline")
    args.execution_ms = teamlib.duration_ms(args.mcp_timeout)
    if args.execution_ms is None:
        p.error("--mcp-timeout must be a finite duration (ms, s, m or h)")
    prompt_ms = teamlib.duration_ms(args.prompt_timeout)
    if prompt_ms is not None and prompt_ms >= args.seconds * 1000:
        p.error("--seconds must exceed --prompt-timeout so a whole PM turn fits in the launcher deadline")
    if args.restart_daemon and not args.port:
        p.error("--restart-daemon needs a fixed --port (agents keep the original MCP URL)")
    if args.workspace and (args.ref or args.model):
        p.error("--ref and --model are for --repo; existing workspaces supply their own configuration")
    if args.pass_env and (len(set(args.pass_env)) != len(args.pass_env) or any(
        not re.fullmatch(r"[A-Z][A-Z0-9_]*_API_KEY", k) or not os.environ.get(k) for k in args.pass_env
    )):
        p.error("use explicit nonempty provider *_API_KEY variable names")
    if not args.check:
        if not args.objective.strip() or not args.accept_provider_data:
            p.error("live run requires an objective and explicit --accept-provider-data")
        if not args.pass_env and not args.public_workspace:
            p.error("credential-free runs require --public-workspace; never send private source to free models")
        pm_prompt(load_roster(args.team), args.objective)
    if args.repo:
        source_url(args.repo)
        model = model_selection(args.model) if args.model else None
    else:
        args.workspace = Path(args.workspace).expanduser().resolve(strict=True)
        _, _, model = inspect_workspace(args.workspace, "http://127.0.0.1:" + str(args.port), args.execution_ms)
    if not args.check and not args.pass_env and model is not None and not (
        model["providerID"] == "opencode" and
        (model["model"] == "big-pickle" or model["model"].endswith("-free"))
    ):
        p.error("credential-free runs require the OpenCode default or an explicitly selected OpenCode free model")
    args.gateway = shutil.which(args.gateway)
    if not args.gateway or subprocess.check_output([args.gateway, "--version"], env=teamlib.clean_environment(), timeout=15, text=True).strip() != "0.9.0":
        p.error("install pinned opencode-a2a-lite 0.9.0")
    if not teamlib.opencode_supported(subprocess.check_output(["opencode", "--version"], env=teamlib.clean_environment(), timeout=15, text=True).strip()):
        p.error("install stable OpenCode 2.3.2 or newer")
    if args.port:
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", args.port))
    os.umask(0o077)
    root = Path(tempfile.mkdtemp(prefix="agentiplex-repository-", dir="/tmp")).resolve()
    if args.workspace and (root == args.workspace or args.workspace in root.parents):
        root.rmdir()
        p.error("private run directory must be outside the selected workspace")
    print("Private run directory:", root, flush=True)
    build_binaries(root)
    if args.repo:
        args.workspace = clone_workspace(root, args.repo, args.ref)
    print("Selected workspace:", args.workspace, flush=True)
    with directory_lock(args.workspace):
        # Lock held across final inspection, startup, execution and evidence.
        if args.repo:
            before = git_snapshot(args.workspace)
        else:
            args.workspace, before, model = inspect_workspace(args.workspace, "http://127.0.0.1:" + str(args.port), args.execution_ms)
        runner = RepositoryTeam(args, {"agents": load_roster(args.team)}, root, time.monotonic() + args.seconds)
        try:
            runner.start_daemon("repository-demo", not args.headless)
            if args.repo:
                prepare_clone_config(args.workspace, runner.endpoint, model, args.execution_ms)
                args.workspace, before, model = inspect_workspace(args.workspace, runner.endpoint, args.execution_ms)
            runner.start_agents()
            if not args.check:
                runner.observe_repository(args.objective)
        finally:
            runner.stop()
            metadata = save_workspace_evidence(args.workspace, before, root, model)
            print("Owned processes stopped. Inspect private evidence at:", root, flush=True)
        if args.check:
            if metadata["statusChanged"]:
                raise RuntimeError("provisioning unexpectedly changed the workspace")
            print("PASS: real gateways provisioned; no model task submitted.")
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
