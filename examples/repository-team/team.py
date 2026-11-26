#!/usr/bin/env python3
"""Operator-owned shared repository example; no workspace handling in Agentiplex."""
import argparse
from contextlib import contextmanager
import fcntl
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
from urllib.parse import urlsplit

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


@contextmanager
def directory_lock(path, blocking=False):
    """Cooperating launcher lock; no lock files in the checkout and no unlink race."""
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | (0 if blocking else fcntl.LOCK_NB))
        except BlockingIOError as exc:
            raise ValueError("workspace already in use by another repository-team run") from exc
        yield
    finally:
        os.close(fd)


def model_selection(value):
    if not re.fullmatch(r"[A-Za-z0-9._:-]+/[A-Za-z0-9._:-]+", value or ""):
        raise ValueError("--model must be provider/model")
    provider, model = value.split("/", 1)
    return {"providerID": provider, "model": model}


def source_url(value):
    """Require a literal Git URL (no embedded HTTP credentials or remote helpers)."""
    if value.startswith(("https://", "ssh://")):
        parsed = urlsplit(value)
        if not parsed.hostname or parsed.password or parsed.query or parsed.fragment or (
            parsed.scheme == "https" and parsed.username
        ):
            raise ValueError("use a Git URL without embedded credentials, query or fragment")
        return value
    if re.fullmatch(r"[A-Za-z0-9_.-]+@[A-Za-z0-9.-]+:[A-Za-z0-9_./-]+", value):
        return value
    if Path(value).is_absolute() and Path(value).is_dir():
        return str(Path(value).resolve())
    raise ValueError("REPO must be an HTTPS/SSH Git URL or an absolute local Git repository path")


def clone_workspace(root, repo, ref=None):
    """One private object database + worktree per run. No cache, shared refs or hardlinks."""
    url = source_url(repo)
    if ref and (ref.startswith("-") or subprocess.run(
        ["git", "check-ref-format", "refs/heads/" + ref], stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL, env=base.clean_environment(), timeout=10
    ).returncode):
        raise ValueError("REF must be a branch or tag name (not an option or revision expression)")
    store = root / "repository.git"
    workspace = root / "workspace"
    # Git auth is operator-side only; never forward provider secrets to clone.
    env = base.clean_environment()
    for key in ("SSH_AUTH_SOCK", "GIT_ASKPASS", "SSH_ASKPASS", "GIT_SSH", "GIT_SSH_COMMAND"):
        if key in os.environ:
            env[key] = os.environ[key]
    env["GIT_TERMINAL_PROMPT"] = "0"
    cmd = ["git", "-c", "core.hooksPath=/dev/null", "-c", "protocol.allow=never",
           "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always",
           "-c", "protocol.file.allow=always", "clone", "--bare", "--no-local",
           "--no-recurse-submodules"]
    if ref:
        cmd += ["--branch", ref]
    subprocess.run(cmd + ["--", url, str(store)], env=env, timeout=300, check=True)
    commit = git(store, "rev-parse", "--verify", "HEAD^{commit}").decode().strip()
    git(store, "-c", "core.hooksPath=/dev/null", "worktree", "add", "--detach", str(workspace), commit)
    if git_snapshot(workspace)["status"]:
        raise RuntimeError("new worktree is not clean")
    # A bare clone retains the source URL for provenance. Disable its push URL
    # as an accident guard, not as a security control against full-access agents.
    git(store, "config", "remote.origin.pushurl", "/dev/null")
    return workspace


def prepare_clone_config(workspace, endpoint, model):
    """Only create an absent config, exclude locally; never overwrite tracked or existing config."""
    for name in ("opencode.json", "opencode.jsonc"):
        path = workspace / name
        if path.exists() or path.is_symlink() or git(workspace, "ls-files", "--", name):
            raise ValueError("cloned project already has OpenCode config; use WORKSPACE with a reviewed configuration")
    exclude = Path(git(workspace, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude").decode().strip())
    exclude.parent.mkdir(parents=True, exist_ok=True)
    with exclude.open("a", encoding="utf-8") as f:
        f.write("\n/opencode.json\n")
    config = {"$schema": "https://opencode.ai/config.json",
              "mcp": {"servers": {"agentiplex": {"type": "remote", "url": endpoint + "/mcp",
                                                  "protocol": "auto", "codemode": False}}}}
    if model is not None:
        config["model"] = model
    with (workspace / "opencode.json").open("x", encoding="utf-8") as f:
        f.write(json.dumps(config, indent=2) + "\n")
    if git_snapshot(workspace)["status"]:
        raise RuntimeError("generated configuration made worktree dirty")


def build_binaries(root):
    # Shared source build outputs are serialized among repository-team launchers;
    # each copies the binaries before releasing the lock.
    env = {k: v for k, v in os.environ.items()
           if not k.endswith("_API_KEY") and not k.startswith("AGENTIPLEX_")
           and k not in {"MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES"}}
    with directory_lock(ROOT, blocking=True):
        subprocess.run(["make", "build"], cwd=ROOT, env=env, timeout=600, check=True)
        for name in ("agentiplexd", "agentiplex"):
            shutil.copy2(ROOT / "bin" / name, root / name)


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
    if model is not None and (not isinstance(model, dict) or set(model) != {"providerID", "model"} or any(
        not isinstance(v, str) or not re.fullmatch(r"[A-Za-z0-9._:-]+", v) for v in model.values()
    )):
        raise ValueError("opencode.json model must be omitted (OpenCode default) or an explicit providerID and model")
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


def log_tail(path, lines=3, width=300):
    try:
        text = path.read_text(errors="replace").strip().splitlines()[-lines:]
    except OSError:
        return ""
    return "".join("\n  log: " + line[:width] for line in text if line.strip())


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
        self.start_daemon()
        self.start_agents()

    def start_daemon(self):
        if not self.args.accept_full_access:
            raise ValueError("full access requires --accept-full-access")
        # Omit public URL so the daemon advertises the actual kernel-assigned port.
        self.spawn("daemon", [str(self.root / "agentiplexd"), "--listen", "127.0.0.1:" + str(self.args.port),
                   "--broker-mode", "embedded", "--namespace", "repository-demo",
                   "--storage", str(self.root / "jetstream"), "--mcp",
                   "--dashboard=" + str(not self.args.headless).lower()],
                   dict(self.env, AGENTIPLEX_DELIVERY_TIMEOUT="30s",
                        AGENTIPLEX_DELIVERY_RETRY_DELAY="2s", AGENTIPLEX_DELIVERY_MAX_ATTEMPTS="80"))
        # Require our own daemon's readiness record even for a fixed port.
        # A preflight bind is only advisory: another process could bind first.
        while True:
            self.alive()
            match = re.search(r'agentiplexd ready"?\s+listen=127\.0\.0\.1:(\d+)\b',
                              (self.root / "daemon.log").read_text(errors="replace"))
            if match:
                port = int(match.group(1))
                if not 1024 <= port <= 65535 or self.args.port not in (0, port):
                    raise RuntimeError("unexpected daemon readiness address")
                self.endpoint = "http://127.0.0.1:" + str(port)
                break
            time.sleep(.2)
        while True:
            self.alive()
            try:
                if base.get(self.endpoint + "/readyz").get("ready"):
                    break
            except (OSError, ValueError):
                pass
            time.sleep(.2)

    def start_agents(self):
        for agent in self.team["agents"]:
            name, skill = agent["id"], agent["skill"]
            profile, policy = policy_for(agent)
            path = self.root / (name + "-policy.json")
            path.write_text(json.dumps(policy, indent=2) + "\n")
            env = dict(self.env, A2A_NAME=name, A2A_DESCRIPTION=ROLES[agent["role"]],
                       A2A_TAGS=skill + ",real-opencode,repository-team")
            for key in self.args.pass_env:
                env[key] = os.environ[key]
            print("Starting", name, "gateway (runtime preflight can take a minute or more)", flush=True)
            proc = self.spawn(name, gateway_command(self.args, self.args.workspace, path, profile),
                              env, self.args.workspace)
            try:
                url = self.gateway_ready(name, proc)
                card = self.wait_card(url, proc, name, skill)
            except RuntimeError as exc:
                raise RuntimeError(str(exc) + log_tail(self.root / (name + ".log"))) from exc
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
    source = p.add_mutually_exclusive_group(required=True)
    source.add_argument("--workspace", help="existing clean Git checkout")
    source.add_argument("--repo", help="Git URL to clone into an independent scratch worktree")
    p.add_argument("--ref", help="branch or tag to clone (default: remote HEAD)")
    p.add_argument("--model", help="provider/model for a cloned workspace; omit to use the OpenCode default")
    p.add_argument("--objective", default="")
    p.add_argument("--port", type=int, help="default 0 (automatic) for --repo, 19080 for --workspace")
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
    if args.port is None:
        args.port = 0 if args.repo else 19080
    if sys.platform != "linux" or not (1024 <= args.port <= 65535 or args.repo and args.port == 0) or not 60 <= args.seconds <= 1800:
        p.error("requires Linux, an unprivileged port (or 0 for --repo), and a 60–1800 second deadline")
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
        pm_prompt(args.objective)
    if args.repo:
        source_url(args.repo)
        model = model_selection(args.model) if args.model else None
    else:
        args.workspace = Path(args.workspace).expanduser().resolve(strict=True)
        _, _, model = inspect_workspace(args.workspace, "http://127.0.0.1:" + str(args.port))
    if not args.check and not args.pass_env and model is not None and not (
        model["providerID"] == "opencode" and
        (model["model"] == "big-pickle" or model["model"].endswith("-free"))
    ):
        p.error("credential-free runs require the OpenCode default or an explicitly selected OpenCode free model")
    args.gateway = shutil.which(args.gateway)
    if not args.gateway or subprocess.check_output([args.gateway, "--version"], env=base.clean_environment(), timeout=15, text=True).strip() != "0.8.3":
        p.error("install pinned opencode-a2a-lite 0.8.3")
    if not base.opencode_supported(subprocess.check_output(["opencode", "--version"], env=base.clean_environment(), timeout=15, text=True).strip()):
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
            args.workspace, before, model = inspect_workspace(args.workspace, "http://127.0.0.1:" + str(args.port))
        team = base.load_team(base.HERE / "team.json", "real")
        runner = RepositoryTeam(args, team, root, time.monotonic() + args.seconds)
        try:
            runner.start_daemon()
            if args.repo:
                prepare_clone_config(args.workspace, runner.endpoint, model)
                args.workspace, before, model = inspect_workspace(args.workspace, runner.endpoint)
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
