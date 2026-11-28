#!/usr/bin/env python3
"""Independently rerun an operator-approved test command in the workspace after reviewing the agents' diff.

Fails on a nonzero exit, a timeout or when the command changes the Git-visible state.
"""
import argparse
import subprocess
import sys


def git(workspace, *args):
    return subprocess.run(["git", "-C", str(workspace), *args], check=True, capture_output=True).stdout


def snapshot(workspace):
    return (git(workspace, "status", "--porcelain=v1", "--untracked-files=all"),
            git(workspace, "diff", "--no-ext-diff", "--binary", "HEAD", "--"))


def verify(workspace, command, seconds):
    before = snapshot(workspace)
    try:
        code = subprocess.run(command, cwd=workspace, timeout=seconds).returncode
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise RuntimeError("verification command failed to complete: " + type(exc).__name__)
    if code != 0:
        raise RuntimeError("verification command exited %d" % code)
    if snapshot(workspace) != before:
        raise RuntimeError("verification command changed the Git-visible workspace state")


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--workspace", required=True)
    p.add_argument("--seconds", type=int, default=600)
    p.add_argument("--accept-run-code", action="store_true", help="confirm you reviewed the diff and trust this command")
    p.add_argument("command", nargs=argparse.REMAINDER)
    args = p.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not args.accept_run_code or not command:
        p.error("--accept-run-code and a command after -- are required")
    try:
        verify(args.workspace, command, args.seconds)
    except RuntimeError as exc:
        print("BLOCKED:", exc, file=sys.stderr)
        sys.exit(1)
    print("PASS: operator command exited zero with unchanged Git state; review its actual output.")


if __name__ == "__main__":
    main()
