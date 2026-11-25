import json
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import team
import verify


class RepositoryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.workspace = self.root / "repo"
        self.workspace.mkdir()
        self.endpoint = "http://127.0.0.1:19080"
        self.config = {
            "model": {"providerID": "opencode", "model": "big-pickle"},
            "mcp": {"servers": {"agentiplex": {
                "type": "remote", "url": self.endpoint + "/mcp",
                "protocol": "auto", "codemode": False,
            }}},
        }
        (self.workspace / "opencode.json").write_text(json.dumps(self.config))
        (self.workspace / "README.md").write_text("before\n")
        for args in (("init", "-q"), ("config", "user.name", "Test"),
                     ("config", "user.email", "test@example.invalid"), ("add", "."),
                     ("commit", "-qm", "initial")):
            subprocess.run(["git", "-C", str(self.workspace), *args], check=True)

    def test_explicit_clean_workspace_and_unchanged_config(self):
        path, before, model = team.inspect_workspace(str(self.workspace), self.endpoint)
        self.assertEqual(self.workspace.resolve(), path)
        self.assertEqual(self.config["model"], model)
        self.assertFalse(before["status"])
        self.assertEqual(self.config, json.loads((self.workspace / "opencode.json").read_text()))
        with self.assertRaises(ValueError):
            team.inspect_workspace(str(self.workspace), "http://127.0.0.1:19081")
        (self.workspace / "README.md").write_text("dirty\n")
        with self.assertRaisesRegex(ValueError, "clean"):
            team.inspect_workspace(str(self.workspace), self.endpoint)

    def test_model_is_optional_and_never_invented(self):
        del self.config["model"]
        (self.workspace / "opencode.json").write_text(json.dumps(self.config))
        subprocess.run(["git", "-C", str(self.workspace), "commit", "-qam", "no model"], check=True)
        _, _, model = team.inspect_workspace(str(self.workspace), self.endpoint)
        self.assertIsNone(model)
        self.config["model"] = {"providerID": "opencode"}
        (self.workspace / "opencode.json").write_text(json.dumps(self.config))
        subprocess.run(["git", "-C", str(self.workspace), "commit", "-qam", "bad model"], check=True)
        with self.assertRaisesRegex(ValueError, "model must be"):
            team.inspect_workspace(str(self.workspace), self.endpoint)

    def test_clone_config_without_model_omits_key(self):
        subprocess.run(["git", "-C", str(self.workspace), "rm", "-q", "opencode.json"], check=True)
        subprocess.run(["git", "-C", str(self.workspace), "commit", "-qm", "rm"], check=True)
        team.prepare_clone_config(self.workspace, self.endpoint, None)
        written = json.loads((self.workspace / "opencode.json").read_text())
        self.assertNotIn("model", written)
        self.assertEqual(self.config["mcp"], written["mcp"])
        self.assertFalse(team.git_snapshot(self.workspace)["status"])

    def test_concurrent_independent_clones_and_clean_worktrees(self):
        # Separate source with no OpenCode config, branch/tag and an uncommitted edit.
        source = self.workspace
        subprocess.run(["git", "-C", str(source), "rm", "opencode.json"], check=True)
        subprocess.run(["git", "-C", str(source), "commit", "-qm", "remove config"], check=True)
        subprocess.run(["git", "-C", str(source), "tag", "demo-tag"], check=True)
        (source / "README.md").write_text("operator dirty change\n")
        original = team.git_snapshot(source)
        roots = [self.root / f"run-{i}" for i in range(2)]
        for root in roots:
            root.mkdir()
        with ThreadPoolExecutor(max_workers=2) as executor:
            workspaces = list(executor.map(lambda root: team.clone_workspace(root, str(source), "demo-tag"), roots))
        self.assertNotEqual(workspaces[0], workspaces[1])
        for workspace, root in zip(workspaces, roots):
            self.assertEqual(root / "workspace", workspace)
            self.assertTrue((workspace / ".git").is_file())
            self.assertEqual("HEAD", team.git_snapshot(workspace)["branch"])
            self.assertEqual(original["head"], team.git_snapshot(workspace)["head"])
            self.assertFalse((root / "repository.git" / "objects" / "info" / "alternates").exists())
            team.prepare_clone_config(workspace, self.endpoint, self.config["model"])
            self.assertFalse(team.inspect_workspace(workspace, self.endpoint)[1]["status"])
            with self.assertRaisesRegex(ValueError, "already has"):
                team.prepare_clone_config(workspace, self.endpoint, self.config["model"])
        (workspaces[0] / "README.md").write_text("first run only\n")
        self.assertEqual("before\n", (workspaces[1] / "README.md").read_text())
        self.assertEqual(original, team.git_snapshot(source))
        with team.directory_lock(workspaces[0]):
            with self.assertRaisesRegex(ValueError, "already in use"):
                with team.directory_lock(workspaces[0]):
                    pass
            with team.directory_lock(workspaces[1]):
                pass
        with team.directory_lock(workspaces[0]):
            pass

    def test_clone_rejects_unsafe_inputs_and_existing_config(self):
        for url in ("ext::sh -c bad", "https://user:secret@example.com/repo", "-option",
                    "https://example.com/repo?token=secret"):
            with self.assertRaises(ValueError):
                team.source_url(url)
        root = self.root / "clone"
        root.mkdir()
        with self.assertRaises(ValueError):
            team.clone_workspace(root, str(self.workspace), "-bad")
        workspace = team.clone_workspace(root, str(self.workspace))
        with self.assertRaisesRegex(ValueError, "already has"):
            team.prepare_clone_config(workspace, self.endpoint, self.config["model"])
        self.assertEqual(self.config, json.loads((workspace / "opencode.json").read_text()))
        self.assertFalse(team.git_snapshot(workspace)["status"])

    def test_same_workspace_and_role_specific_grants(self):
        agents = team.base.load_team(team.base.HERE / "team.json", "real")["agents"]
        self.assertEqual(6, len(agents))
        self.assertTrue(all(a["runtime"] == "opencode" for a in agents))
        args = SimpleNamespace(gateway="opencode-a2a-lite", prompt_timeout="10m", pass_env=["EXAMPLE_API_KEY"], accept_full_access=True)
        for a in agents:
            profile, policy = team.policy_for(a)
            self.assertEqual("full-access" if a["role"] in {"developer", "tester"} else "read-only", profile)
            self.assertEqual(profile, policy["base"])
            resources = {p["resource"] for p in policy["policies"]}
            self.assertIn("agentiplex_get_state:*", resources)
            self.assertIn("agentiplex_put_state:*", resources)
            for tool in team.base.DELEGATE_TOOLS:
                self.assertEqual(a["id"] == "pm", "agentiplex_" + tool + ":*" in resources)
            cmd = team.gateway_command(args, self.workspace, self.root / "policy.json", profile)
            self.assertEqual(str(self.workspace), cmd[cmd.index("--workspace") + 1])
            self.assertEqual("reject", cmd[cmd.index("--permissions") + 1])
            self.assertEqual(profile == "full-access", "--dangerously-skip-permissions" in cmd)
            self.assertEqual(profile, cmd[cmd.index("--policy") + 1])
            self.assertEqual(["--pass-env", "EXAMPLE_API_KEY"], cmd[-2:])

    def test_full_access_requires_acknowledgement(self):
        args = SimpleNamespace(gateway="gateway", prompt_timeout="10m", pass_env=[], accept_full_access=False)
        with self.assertRaisesRegex(ValueError, "--accept-full-access"):
            team.gateway_command(args, self.workspace, self.root / "policy.json", "full-access")
        # Entry point also fails before accessing a checkout or starting anything.
        result = subprocess.run([sys.executable, "-B", str(Path(team.__file__)), "--workspace",
                                 "/nonexistent", "--check"], capture_output=True, text=True)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("--accept-full-access", result.stderr)
        self.assertIn("NO OS sandbox", result.stderr)

    def test_start_registers_only_endpoints_with_same_workspace(self):
        args = SimpleNamespace(port=19080, headless=True, gateway="gateway", prompt_timeout="10m",
                               pass_env=[], workspace=self.workspace, accept_full_access=True)
        roster = team.base.load_team(team.base.HERE / "team.json", "real")
        runner = team.RepositoryTeam(args, roster, self.root, float("inf"))
        commands, calls = [], []

        def spawn(name, cmd, env, cwd=None):
            commands.append((name, cmd, env, cwd))
            if name == "daemon":
                (self.root / "daemon.log").write_text("INFO agentiplexd ready listen=127.0.0.1:19080 broker_mode=embedded\n")
            return name

        def cli(*args):
            calls.append(args)
            if args == ("agents", "list"):
                return [{"id": a["id"]} for a in roster["agents"]]

        def card(url, proc, name, skill):
            return {"skills": [{"tags": [skill, "real-opencode"]}]}

        with patch.object(runner, "spawn", side_effect=spawn), patch.object(runner, "cli", side_effect=cli), \
             patch.object(runner, "gateway_ready", side_effect=lambda n, p: "http://127.0.0.1:200" + str(len(commands))), \
             patch.object(runner, "wait_card", side_effect=card), \
             patch.object(team.base, "get", side_effect=lambda url: {"ready": True} if url.endswith("/readyz") else []):
            runner.start()
        self.assertEqual(7, len(commands))
        self.assertNotIn(str(self.workspace), commands[0][1])
        for name, cmd, env, cwd in commands[1:]:
            self.assertEqual(self.workspace, cwd)
            self.assertEqual(str(self.workspace), cmd[cmd.index("--workspace") + 1])
            self.assertEqual(name, env["A2A_NAME"])
        registrations = [c for c in calls if c[0] == "register"]
        self.assertEqual(6, len(registrations))
        self.assertTrue(all(len(c) == 3 and c[2].startswith("http://127.0.0.1:") for c in registrations))
        self.assertFalse(team.git_snapshot(self.workspace)["status"])

    def test_daemon_auto_port_uses_owned_readiness_record(self):
        args = SimpleNamespace(port=0, headless=True, accept_full_access=True)
        runner = team.RepositoryTeam(args, {}, self.root, float("inf"))
        (self.root / "daemon.log").write_text("INFO agentiplexd ready listen=127.0.0.1:29451 broker_mode=embedded\n")
        with patch.object(runner, "spawn") as spawn, patch.object(team.base, "get", return_value={"ready": True}) as get:
            runner.start_daemon()
            cmd = spawn.call_args.args[1]
            self.assertEqual("127.0.0.1:0", cmd[cmd.index("--listen") + 1])
            self.assertNotIn("--public-url", cmd)
            get.assert_called_once_with("http://127.0.0.1:29451/readyz")
        self.assertEqual("http://127.0.0.1:29451", runner.endpoint)
        args.port = 19080
        with patch.object(runner, "spawn"), patch.object(team.base, "get") as get:
            with self.assertRaisesRegex(RuntimeError, "unexpected daemon"):
                runner.start_daemon()
            get.assert_not_called()

    def evidence(self):
        root = {"id": "root", "target": "pm", "state": "completed", "result": {"text": "done"}}
        return {"root": root, "deliveries": [root, *[
            {"id": role, "target": role, "state": "completed", "result": {"text": "finding"}}
            for role in ("architect", "developer", "tester-1", "quality")
        ]], "state": [{"key": k, "revision": 1, "value": "note"} for k in ("pm", "developer", "tester-1")]}

    def test_acceptance_requires_real_receipts_and_state(self):
        e = self.evidence()
        self.assertIn("developer", team.verify_collaboration(e))
        e["deliveries"][-1]["state"] = "accepted"
        with self.assertRaisesRegex(RuntimeError, "did not complete"):
            team.verify_collaboration(e)
        e["deliveries"][-1]["state"] = "completed"
        e["deliveries"][-1].pop("result")
        with self.assertRaisesRegex(RuntimeError, "missing completed"):
            team.verify_collaboration(e)
        e = self.evidence()
        e["state"].pop()
        with self.assertRaisesRegex(RuntimeError, "shared-state"):
            team.verify_collaboration(e)
        e = self.evidence()
        e["state"] = [x for x in e["state"] if x["key"] != "tester-1"]
        with self.assertRaisesRegex(RuntimeError, "tester shared-state"):
            team.verify_collaboration(e)
        self.assertIn("NOT RUN", team.pm_prompt("Improve README"))
        self.assertIn("exit codes", team.pm_prompt("Improve README"))
        self.assertIn("retest", team.pm_prompt("Improve README"))
        with self.assertRaises(ValueError):
            team.pm_prompt("x" * 4096)

    def test_git_evidence_and_operator_verification(self):
        before = team.git_snapshot(self.workspace)
        (self.workspace / "README.md").write_text("after\n")
        evidence = self.root / "evidence"
        evidence.mkdir()
        metadata = team.save_workspace_evidence(self.workspace, before, evidence, self.config["model"])
        self.assertTrue(metadata["trackedDiff"])
        self.assertIn(b"+after", (evidence / "changes.diff").read_bytes())
        verify.verify(self.workspace, evidence, [sys.executable, "-c", "print('actual test output')"], 10)
        result = list(evidence.glob("verification-*/result.json"))[0]
        self.assertEqual(0, json.loads(result.read_text())["exitCode"])
        self.assertIn("actual test output", result.with_name("command.log").read_text())
        with self.assertRaisesRegex(RuntimeError, "verification failed"):
            verify.verify(self.workspace, evidence, [sys.executable, "-c", "raise SystemExit(3)"], 10)
        (self.workspace / "README.md").write_text("changed again\n")
        with self.assertRaisesRegex(ValueError, "differs"):
            verify.verify(self.workspace, evidence, [sys.executable, "-c", "pass"], 10)
        subprocess.run(["git", "-C", str(self.workspace), "add", "README.md"], check=True)
        with self.assertRaisesRegex(RuntimeError, "index"):
            team.save_workspace_evidence(self.workspace, before, evidence, self.config["model"])


if __name__ == "__main__":
    unittest.main()
