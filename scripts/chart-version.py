#!/usr/bin/env python3
"""Synchronize the two top-level Helm version fields without rewriting YAML."""
import re
import sys
from pathlib import Path

SEMVER = r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
CHART = Path("deploy/helm/agentiplex/Chart.yaml")


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in ("preview", "update", "check"):
        raise ValueError("Usage: chart-version.py preview|update|check MAJOR.MINOR.PATCH")
    mode, version = sys.argv[1:]
    if not re.fullmatch(SEMVER, version):
        raise ValueError(f"Invalid stable version: {version}")
    text = CHART.read_text(encoding="utf-8")
    lines = text.splitlines(keepends=True)
    for key, new in (("version", version), ("appVersion", f'"{version}"')):
        matches = [i for i, line in enumerate(lines) if re.match(rf"^{key}\s*:", line)]
        if len(matches) != 1:
            raise ValueError(f"{CHART}: expected exactly one top-level {key}")
        i = matches[0]
        match = re.fullmatch(
            rf"({key}:[ \t]*)(?:({SEMVER})|\"({SEMVER})\"|'({SEMVER})')([ \t]*(?:#[^\n]*)?)(\n?)",
            lines[i],
        )
        if not match:
            raise ValueError(f"{CHART}: invalid stable {key}")
        old = next(value for value in match.group(2, 3, 4) if value is not None)
        if mode == "check" and old != version:
            raise ValueError(f"{CHART}: {key} is {old}, expected {version}")
        if mode == "preview":
            print(f"{CHART}: {key} {old} -> {version}")
        lines[i] = match.group(1) + new + match.group(5) + match.group(6)
    updated = "".join(lines)
    if mode == "update" and updated != text:
        CHART.write_text(updated, encoding="utf-8")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as exc:
        sys.exit(str(exc))
