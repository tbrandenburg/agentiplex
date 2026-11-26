#!/usr/bin/env python3
"""Run an explicitly operator-approved test command after reviewing the model's diff."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

import repo


def verify(workspace, run_dir, command, seconds):
    metadata = json.loads((run_dir / "workspace.json").read_text())
    if workspace != Path(metadata["workspace"]).resolve():
        raise ValueError("workspace does not match the recorded run")
    before = repo.git_snapshot(workspace)
    if (before["head"] != metadata["finalHead"] or before["branch"] != metadata["finalBranch"] or
            before["index"] or before["status"] != (run_dir / "git-status.txt").read_bytes() or
            before["diff"] != (run_dir / "changes.diff").read_bytes()):
        raise ValueError("workspace differs from recorded Git evidence; inspect changes before testing")
    # This is operator execution, never an agent-generated command or shell eval.
    # Even an argv command executes potentially modified repository code.
    os.umask(0o077)
    out = Path(tempfile.mkdtemp(prefix="verification-", dir=run_dir))
    code = None
    error = None
    with (out / "command.log").open("wb") as log:
        try:
            code = subprocess.run(command, cwd=workspace, env=repo.clean_environment(),
                                  stdout=log, stderr=subprocess.STDOUT, timeout=seconds).returncode
        except (OSError, subprocess.TimeoutExpired) as exc:
            error = type(exc).__name__
    after = repo.git_snapshot(workspace)
    unchanged = before == after
    (out / "git-status-after.txt").write_bytes(after["status"])
    (out / "changes-after.diff").write_bytes(after["diff"])
    result = {"command": command, "exitCode": code, "error": error,
              "gitEvidenceUnchanged": unchanged, "untrackedContentsVerified": False}
    (out / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    print("Verification evidence:", out, flush=True)
    if error or code != 0 or not unchanged:
        raise RuntimeError("verification failed or changed Git-visible evidence; inspect log")
    print("PASS: operator command exited zero; review its actual output.")


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--workspace", required=True)
    p.add_argument("--run-dir", required=True)
    p.add_argument("--seconds", type=int, default=600)
    p.add_argument("--accept-run-code", action="store_true",
                   help="confirm you reviewed the diff and trust this test command and code")
    p.add_argument("command", nargs=argparse.REMAINDER)
    args = p.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not args.accept_run_code or not command or not 1 <= args.seconds <= 1800:
        p.error("explicit --accept-run-code, a command after --, and a bounded deadline are required")
    verify(Path(args.workspace).expanduser().resolve(strict=True),
           Path(args.run_dir).expanduser().resolve(strict=True), command, args.seconds)


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print("BLOCKED:", exc, file=sys.stderr)
        sys.exit(1)
