import json
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

    def test_same_workspace_and_role_specific_grants(self):
        agents = team.base.load_team(team.base.HERE / "team.json", "real")["agents"]
        self.assertEqual(6, len(agents))
        self.assertTrue(all(a["runtime"] == "opencode" for a in agents))
        args = SimpleNamespace(gateway="opencode-a2a-lite", prompt_timeout="10m", pass_env=["EXAMPLE_API_KEY"])
        for a in agents:
            profile, policy = team.policy_for(a)
            self.assertEqual("edit" if a["id"] == "developer" else "read-only", profile)
            self.assertEqual(profile, policy["base"])
            resources = {p["resource"] for p in policy["policies"]}
            self.assertIn("agentiplex_get_state:*", resources)
            self.assertIn("agentiplex_put_state:*", resources)
            for tool in team.base.DELEGATE_TOOLS:
                self.assertEqual(a["id"] == "pm", "agentiplex_" + tool + ":*" in resources)
            cmd = team.gateway_command(args, self.workspace, self.root / "policy.json", profile)
            self.assertEqual(str(self.workspace), cmd[cmd.index("--workspace") + 1])
            self.assertEqual("reject", cmd[cmd.index("--permissions") + 1])
            self.assertNotIn("full-access", cmd)
            self.assertNotIn("--dangerously-skip-permissions", cmd)
            self.assertEqual(["--pass-env", "EXAMPLE_API_KEY"], cmd[-2:])

    def test_start_registers_only_endpoints_with_same_workspace(self):
        args = SimpleNamespace(port=19080, headless=True, gateway="gateway", prompt_timeout="10m",
                               pass_env=[], workspace=self.workspace)
        roster = team.base.load_team(team.base.HERE / "team.json", "real")
        runner = team.RepositoryTeam(args, roster, self.root, float("inf"))
        commands, calls = [], []

        def spawn(name, cmd, env, cwd=None):
            commands.append((name, cmd, env, cwd))
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

    def evidence(self):
        root = {"id": "root", "target": "pm", "state": "completed", "result": {"text": "done"}}
        return {"root": root, "deliveries": [root, *[
            {"id": role, "target": role, "state": "completed", "result": {"text": "finding"}}
            for role in ("architect", "developer", "tester-1", "quality")
        ]], "state": [{"key": k, "revision": 1, "value": "note"} for k in ("pm", "developer")]}

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
        self.assertIn("NOT RUN", team.pm_prompt("Improve README"))
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
