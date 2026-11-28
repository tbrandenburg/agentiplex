#!/usr/bin/env python3
"""Provision five real, explicitly prompted gateways on a synthetic Git workspace.
By default submit no model task; --live selects a bounded real-model acceptance scenario. Test-only.
"""
import argparse
import json
import re
import subprocess
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import procs
import verify

ROOT = Path(__file__).resolve().parents[3]
EX = ROOT / "examples" / "repository-team"
POLICY = {"pm": ("coordinator", "read"), "architect": ("reader", "read"), "developer": ("writer", "danger-full-access"),
          "tester": ("writer", "danger-full-access"), "quality": ("reader", "read")}
DESCRIPTIONS = {
    "pm": "Engineering coordination and objective ownership",
    "architect": "Architecture analysis and technical recommendations",
    "developer": "Repository implementation and repair",
    "tester": "Independent executable verification",
    "quality": "Independent change and acceptance review"
}
PORTS = {"pm": 19081, "architect": 19082, "developer": 19083, "tester": 19084, "quality": 19085}
RETRIES = 4  # the gateway preflight is known to be flaky
TERMINAL = {"completed", "failed", "partial", "uncertain", "input-required", "auth-required"}


def git(ws, *a):
    return subprocess.run(["git", "-C", str(ws), *a], check=True, capture_output=True, text=True).stdout


def fixture(ws, model):
    # All objectives use the same starting repository and the same role files.
    (ws / "README.md").write_text("# Synthetic repository\n\n[Usage](docs/missing.md)\n")
    (ws / "docs").mkdir()
    (ws / "docs" / "usage.md").write_text("Call clamp(value, lower, upper).\n")
    (ws / "calc.py").write_text("def clamp(value, lower, upper):\n    return min(lower, max(upper, value))\n")
    # A real, visible failing check that the agents can reproduce. The
    # independent verifier separately owns its assertions outside the workspace.
    (ws / "test_calc.py").write_text(
        "from calc import clamp\n\n"
        "assert [clamp(v, 0, 10) for v in (-3, 0, 4, 10, 12)] == [0, 0, 4, 10, 10]\n"
        "assert clamp(-2, -5, -1) == -2\n"
        "assert clamp(5, 2, 2) == 2\n"
        "print('clamp checks passed')\n"
    )
    (ws / ".gitignore").write_text("__pycache__/\n")
    cfg = json.loads((EX / "opencode.json").read_text())
    if model:
        provider, name = model.split("/", 1)
        cfg["model"] = {"providerID": provider, "model": name}
    (ws / "opencode.json").write_text(json.dumps(cfg, indent=2) + "\n")


def submit_objective(cli, objective):
    # Copy exact bytes deterministically using native create-only state before
    # sending the same text to the PM. No model is responsible for transcription.
    initial = cli("state", "put", "repository", "objective", objective, "--revision", "0")
    revision = initial.get("revision", 0)
    if initial.get("value") != objective or revision <= 0:
        raise RuntimeError("operator objective publication failed")
    return cli("send", "pm", objective, "--idempotency-key", "repository-objective")["id"], revision


def verify_evidence(root, deliveries, state, objective, objective_revision, integration=False):
    # Local mode has no authenticated author. Topics are evidence, not identity attestation.
    if root["target"] != "pm" or root["state"] != "completed" or not root.get("result") or root.get("text") != objective:
        raise RuntimeError("coordinator did not produce a completed result")
    if any(d["state"] != "completed" for d in deliveries):
        raise RuntimeError("a delivery did not complete successfully; inspect evidence")
    workers = {d.get("assignedAgent") or d["target"] for d in deliveries
               if d["id"] != root["id"] and d.get("result")}
    reviewers = workers & {"tester", "quality"}
    if "developer" not in workers or not reviewers:
        raise RuntimeError("missing real implementer or independent reviewer delivery")
    if integration and reviewers != {"tester", "quality"}:
        raise RuntimeError("integration requires independent tester and quality results")
    if not any(s["key"] == "objective" and s.get("value") == objective and s.get("revision") == objective_revision for s in state):
        raise RuntimeError("original shared objective missing, altered or rewritten")
    topics = {role for role in {"pm", "developer", *reviewers}
              if any(s["key"].startswith("note-" + role + "-") and s.get("value") and s.get("revision", 0) > 0
                     and json.loads(s["value"]).get("text", "").strip() for s in state)}
    if not {"pm", "developer"} <= topics or not topics & reviewers:
        raise RuntimeError("missing coordinator, implementer or reviewer notes")
    if integration and not reviewers <= topics:
        raise RuntimeError("integration missing tester or quality findings")
    return sorted(workers)


def verify_merge(ws, base, branch):
    if branch != "main" or git(ws, "branch", "--show-current").strip() != "main":
        raise RuntimeError("integration must finish on main")
    if git(ws, "status", "--porcelain") or git(ws, "remote").strip():
        raise RuntimeError("integration must finish clean with no remote")
    parents = git(ws, "rev-list", "--parents", "-n", "1", "HEAD").split()
    if len(parents) != 3 or parents[1] != base or parents[2] == base:
        raise RuntimeError("expected a real two-parent merge into the original main")
    candidate = parents[2]
    if not git(ws, "for-each-ref", "--points-at=" + candidate, "--format=%(refname:short)", "refs/heads").strip():
        raise RuntimeError("expected retained feature branch at the merged candidate")
    # The reviewed candidate must contain the initial base and exactly the
    # integrated tree; no surprise edits introduced by the merge itself.
    if git(ws, "merge-base", base, candidate).strip() != base:
        raise RuntimeError("candidate is not descended from original main")
    if git(ws, "diff", "--name-only", candidate, "HEAD").strip():
        raise RuntimeError("merge introduced changes beyond the candidate")
    if git(ws, "diff", "--name-only", base, candidate).splitlines() != ["calc.py"]:
        raise RuntimeError("candidate changed files outside the objective")
    print("Verified local merge:", parents[0], "candidate:", candidate, flush=True)


def verify_workspace(ws, head, branch, scenario):
    head, branch = head.strip(), branch.strip()
    wanted = {"docs": "README.md", "defect": "calc.py", "integration": "calc.py"}[scenario]
    if scenario == "integration":
        verify_merge(ws, head, branch)
    else:
        if git(ws, "rev-parse", "HEAD").strip() != head or git(ws, "rev-parse", "--abbrev-ref", "HEAD").strip() != branch:
            raise RuntimeError("agents changed Git history or branch")
        if git(ws, "for-each-ref", "--format=%(refname:short)", "refs/heads").splitlines() != [branch]:
            raise RuntimeError("patch-only objective created additional branches")
    changed = git(ws, "diff", "--name-only", head).splitlines()
    if changed != [wanted] or git(ws, "diff", "--cached", "--name-only") or git(ws, "ls-files", "--others", "--exclude-standard"):
        raise RuntimeError("unexpected workspace scope, staged or untracked changes")
    git(ws, "diff", "--check", head)
    if (ws / wanted).is_symlink() or not (ws / wanted).is_file():
        raise RuntimeError("changed file must be a regular file")
    if scenario == "docs":
        if (ws / "README.md").read_text() not in ("# Synthetic repository\n\n[Usage](docs/usage.md)\n",
                                                   "# Synthetic repository\n\n[Usage](./docs/usage.md)\n"):
            raise RuntimeError("README link not repaired while preserving unrelated content")
    else:
        # Fixed operator-owned checks, not a model-authored command or test.
        check = ("import runpy, sys; clamp = runpy.run_path(sys.argv[1])['clamp']; "
                 "assert [clamp(v, 0, 10) for v in (-3, 0, 4, 10, 12)] == [0, 0, 4, 10, 10]; "
                 "assert clamp(-2, -5, -1) == -2; assert clamp(5, 2, 2) == 2")
        verify.verify(ws, [sys.executable, "-I", "-B", "-c", check, str(ws / "calc.py")], 30)
        verify.verify(ws, [sys.executable, "-B", "test_calc.py"], 30)


def live_check(args, run, ws, api, daemon, head, branch):
    cli = lambda *a: json.loads(subprocess.check_output([str(run / "agentiplex"), "--server", api, *a],
                                                       env=procs.clean_env(), timeout=30))
    objective = {
        "docs": (EX / "objective.md").read_text().rstrip("\n"),
        "defect": "Fix clamp(value, lower, upper) in calc.py for lower <= upper so it returns the value bounded by the inclusive limits. Preserve its signature and leave unrelated files unchanged. The check python3 -B test_calc.py must pass. Leave the patch uncommitted; no branch or history changes.",
        "integration": (EX / "objective.integration.md").read_text().rstrip("\n"),
    }[args.live]
    if args.live != "docs":
        baseline = subprocess.run([sys.executable, "-B", "test_calc.py"], cwd=ws, timeout=30,
                                  capture_output=True, text=True)
        if baseline.returncode != 1 or "AssertionError" not in baseline.stderr:
            raise RuntimeError("fixture must start with the expected failing check")
        print("Baseline local check failed as expected: python3 -B test_calc.py (exit 1)", flush=True)
    root_id, objective_revision = submit_objective(cli, objective)
    end = time.monotonic() + args.seconds
    while time.monotonic() < end:
        if daemon.poll() is not None:
            raise RuntimeError("daemon exited")
        records = procs.get(api + "/api/v1/deliveries?limit=100")
        if len(records) >= 100:
            raise RuntimeError("delivery evidence exceeds bounded acceptance window")
        if any(d["id"] == root_id and d["state"] in TERMINAL for d in records) and all(d["state"] in TERMINAL for d in records):
            break
        time.sleep(1)
    else:
        raise TimeoutError("repository team deadline exceeded")
    ids = {d["id"] for d in records}
    ids.update(child["id"] for d in records for child in d.get("children", []))
    deliveries = [cli("messages", "status", id) for id in sorted(ids)]
    root = next(d for d in deliveries if d["id"] == root_id)
    state = [cli("state", "get", "repository", s["key"]) for s in cli("state", "list", "repository")]
    # Keep raw synthetic evidence private in the run directory, never print it in CI.
    (run / "evidence.json").write_text(json.dumps({"root": root, "deliveries": deliveries, "state": state, "objective_revision": objective_revision}, indent=2))
    print("Delivery summary:", json.dumps([{"id": d["id"], "target": d["target"], "state": d["state"],
                                              "has_result": bool(d.get("result"))} for d in deliveries]), flush=True)
    print("Shared state keys:", json.dumps(sorted(s["key"] for s in state)), flush=True)
    # Only classify public synthetic final-result indicators; never log raw model messages or reasoning.
    result_text = json.dumps(root.get("result", {})).lower()
    print("PM result indicators:", json.dumps({term: term in result_text for term in
          ("blocked", "mcp", "not available", "unavailable", "connection", "cannot", "permission")}), flush=True)
    workers = verify_evidence(root, deliveries, state, objective, objective_revision, integration=args.live == "integration")
    verify_workspace(ws, head, branch, args.live)
    print("PASS:", args.live, "objective; completed worker deliveries:", ", ".join(workers),
          "; original objective and notes persisted; independent workspace check passed.")
    print("Trusted-local evidence: note topics are not authenticated authors; delivery records do not attest tool calls. Review private evidence and diff.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", choices=("docs", "defect", "integration"), help="submit one real objective on a disposable synthetic checkout")
    parser.add_argument("--model", help="provider/model (omit for the OpenCode default)")
    parser.add_argument("--seconds", type=int, default=1200)
    args = parser.parse_args()
    if args.seconds <= 0 or args.seconds > 2400:
        parser.error("--seconds must be between 1 and 2400")
    run = Path(tempfile.mkdtemp(prefix="agentiplex-repository-check-"))
    print("Private run directory:", run, flush=True)
    ws = run / "workspace"
    ws.mkdir()
    git(ws, "init", "-q", "-b", "main")
    git(ws, "config", "user.name", "Example CI")
    git(ws, "config", "user.email", "ci@example.invalid")
    fixture(ws, args.model)
    git(ws, "add", ".")
    git(ws, "commit", "-qm", "synthetic fixture")
    head, branch = git(ws, "rev-parse", "HEAD"), git(ws, "rev-parse", "--abbrev-ref", "HEAD")
    team = (EX / "team.yaml").read_text()
    (run / "team.yaml").write_text(team.replace("/tmp/agentiplex-repository/", str(run) + "/"))
    ports = PORTS
    api = "http://127.0.0.1:%d" % int(re.search(r'listen: "127\.0\.0\.1:(\d+)"', team).group(1))
    for name in ("agentiplexd", "agentiplex"):
        subprocess.run(["go", "build", "-mod=readonly", "-o", str(run / name), "./cmd/" + name], cwd=ROOT, check=True, timeout=300)
    procs.private_policies(EX / "policies", run)
    owned = procs.Procs(str(run))
    try:
        # The explicit-role readiness probe opens an ACP session, so MCP must already be reachable.
        daemon = owned.spawn("daemon", [str(run / "agentiplexd"), "--config", str(run / "team.yaml")])
        owned.wait_url("daemon", daemon, api + "/readyz", 120)
        for name, (policy, access) in POLICY.items():
            env = procs.clean_env(A2A_NAME=name, A2A_DESCRIPTION=DESCRIPTIONS[name], A2A_TAGS=name + ",real-opencode")
            for attempt in range(RETRIES + 1):
                proc = owned.spawn(name, [*procs.gateway_command(), "--workspace", str(ws), "--port", str(ports[name]), "--access", access,
                                         "--policy-file", str(run / "policies" / (policy + ".json")),
                                         "--system-prompt-file", str(EX / "roles" / (name + ".md")),
                                         "--prompt-timeout", "45m", "--no-env"], env, ws)
                try:
                    owned.wait_url(name, proc, "http://127.0.0.1:%d/.well-known/agent-card.json" % ports[name], 600)
                    break
                except RuntimeError:
                    if attempt == RETRIES:
                        raise
                    print(name, "startup failed (known flaky preflight); retrying", flush=True)
                    time.sleep(2)
            subprocess.run([str(run / "agentiplex"), "--server", api, "register", name,
                            "http://127.0.0.1:%d" % ports[name]], env=procs.clean_env(), check=True,
                           stdout=subprocess.DEVNULL, timeout=30)
        agents = json.loads(subprocess.check_output([str(run / "agentiplex"), "--server", api, "agents", "list"], timeout=30))
        if {a["id"] for a in agents} != set(ports):
            raise RuntimeError("registry mismatch")
        if git(ws, "status", "--porcelain"):
            raise RuntimeError("provisioning changed the workspace")
        print("PASS: MCP ready before five explicitly prompted gateways started and registered; no model task submitted.", flush=True)
        if args.live:
            live_check(args, run, ws, api, daemon, head, branch)
    finally:
        owned.stop()
        print("Owned processes stopped. Review/delete:", run, flush=True)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, TimeoutError, subprocess.SubprocessError) as exc:
        print("BLOCKED:", exc, file=sys.stderr)
        sys.exit(1)
