"""Git, clone, lock and workspace-evidence helpers for the repository team (operator-side)."""
from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from urllib.parse import urlsplit

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE.parent))
from teamlib import clean_environment


def git(workspace, *args):
    return subprocess.check_output(
        ["git", "-C", str(workspace), "-c", "core.fsmonitor=false", *args],
        timeout=30, env=clean_environment(),
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
        stderr=subprocess.DEVNULL, env=clean_environment(), timeout=10
    ).returncode):
        raise ValueError("REF must be a branch or tag name (not an option or revision expression)")
    store = root / "repository.git"
    workspace = root / "workspace"
    # Git auth is operator-side only; never forward provider secrets to clone.
    env = clean_environment()
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


def mcp_config(endpoint, execution_ms):
    return {"timeout": {"execution": execution_ms}, "servers": {"agentiplex": {
        "type": "remote", "url": endpoint + "/mcp", "protocol": "auto", "codemode": False}}}


def prepare_clone_config(workspace, endpoint, model, execution_ms):
    """Only create an absent config, exclude locally; never overwrite tracked or existing config."""
    for name in ("opencode.json", "opencode.jsonc"):
        path = workspace / name
        if path.exists() or path.is_symlink() or git(workspace, "ls-files", "--", name):
            raise ValueError("cloned project already has OpenCode config; use WORKSPACE with a reviewed configuration")
    exclude = Path(git(workspace, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude").decode().strip())
    exclude.parent.mkdir(parents=True, exist_ok=True)
    with exclude.open("a", encoding="utf-8") as f:
        f.write("\n/opencode.json\n")
    config = {"$schema": "https://opencode.ai/config.json", "mcp": mcp_config(endpoint, execution_ms)}
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


def inspect_workspace(path, endpoint, execution_ms):
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
    expected = mcp_config(endpoint, execution_ms)
    if config.get("mcp") != expected:
        raise ValueError("opencode.json mcp must be exactly the Agentiplex server for this port plus timeout.execution (ms) matching --mcp-timeout")
    return workspace, before, model


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
