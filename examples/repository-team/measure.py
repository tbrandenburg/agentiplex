#!/usr/bin/env python3
"""Repeat the repository team N times on its default settings and tabulate the outcomes.

Everything after `--` is passed to team.py unchanged (put --repo/--objective/--model etc. there).
"""
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

HERE = Path(__file__).resolve().parent


def summarize(index, code, seconds, output):
    run = re.search(r"Private run directory: (\S+)", output)
    row = {"run": index, "result": "PASS" if code == 0 else "BLOCKED", "wallSeconds": round(seconds),
           "phase": "collaboration" if "Team ready" in output else "startup",
           "runDirectory": run.group(1) if run else None, "reason": None}
    if code:
        row["reason"] = (output.strip().splitlines() or [""])[-1][:200]
    evidence = Path(row["runDirectory"] or "/nonexistent") / "evidence.json"
    if evidence.is_file():
        data = json.loads(evidence.read_text())
        calls = data["toolCalls"]
        workers = {d.get("assignedAgent") or d["target"] for d in data["deliveries"] if d["state"] == "completed" and d.get("result")}
        row.update(pmTurns=calls["coordinatorTurns"], pmAskCalls=calls["askCalls"],
                   pmDelegations=sum(calls["delegations"].values()), qualityRan="quality" in workers,
                   daemonRestarts=data.get("daemonRestarts", 0))
    return row


def table(rows):
    cols = ["run", "result", "phase", "wallSeconds", "pmTurns", "pmAskCalls", "pmDelegations", "qualityRan", "startupAttempts", "reason"]
    lines = ["| " + " | ".join(cols) + " |", "|" + "---|" * len(cols)]
    lines += ["| " + " | ".join(str(r.get(c, "")) if r.get(c) is not None else "" for c in cols) + " |" for r in rows]
    passed = sum(r["result"] == "PASS" for r in rows)
    lines.append("\n%d/%d PASS, %d BLOCKED" % (passed, len(rows), len(rows) - passed))
    return "\n".join(lines) + "\n"


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("-n", type=int, default=10, help="number of consecutive runs")
    p.add_argument("--out", help="results directory (default: private directory under /tmp)")
    p.add_argument("--startup-retries", type=int, default=4,
                   help="relaunch a run that failed before the team was ready (known flaky gateway preflight); not counted as BLOCKED")
    p.add_argument("--run-timeout", type=int, default=4200, help="hard cap per run in seconds")
    p.add_argument("team_args", nargs=argparse.REMAINDER)
    args = p.parse_args()
    team_args = args.team_args[1:] if args.team_args[:1] == ["--"] else args.team_args
    out = Path(args.out) if args.out else Path(tempfile.mkdtemp(prefix="agentiplex-repository-measure-", dir="/tmp"))
    out.mkdir(parents=True, exist_ok=True)
    rows = []
    for i in range(1, args.n + 1):
        for attempt in range(args.startup_retries + 1):
            start = time.monotonic()
            try:
                done = subprocess.run([sys.executable, "-B", str(HERE / "team.py"), *team_args], text=True,
                                      stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=args.run_timeout)
                code, output = done.returncode, done.stdout
            except subprocess.TimeoutExpired as exc:
                code, output = 124, (exc.stdout or b"").decode(errors="replace") + "\nBLOCKED: run exceeded --run-timeout"
            (out / ("run-%02d-attempt-%d.log" % (i, attempt + 1))).write_text(output)
            if code == 0 or "Team ready" in output:
                break
            print("run %d attempt %d failed during startup (known flaky preflight); retrying" % (i, attempt + 1), flush=True)
        rows.append(dict(summarize(i, code, time.monotonic() - start, output), startupAttempts=attempt + 1))
        (out / "results.json").write_text(json.dumps(rows, indent=2) + "\n")
        (out / "results.md").write_text(table(rows))
        print(json.dumps(rows[-1]), flush=True)
    print(table(rows) + "Results:", out, flush=True)


if __name__ == "__main__":
    main()
