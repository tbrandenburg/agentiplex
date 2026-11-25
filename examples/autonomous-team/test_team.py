import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("team", Path(__file__).with_name("team.py"))
team = importlib.util.module_from_spec(spec)
spec.loader.exec_module(team)


class TeamTests(unittest.TestCase):
    def test_profiles(self):
        path = team.HERE / "team.json"
        configured = team.load_team(path, "configured")
        self.assertEqual(2, sum(a["runtime"] == "opencode" for a in configured["agents"]))
        self.assertTrue(all(a["runtime"] == "simulated" for a in team.load_team(path, "simulated")["agents"]))
        self.assertTrue(all(a["runtime"] == "opencode" for a in team.load_team(path, "real")["agents"]))
        self.assertEqual(["tester-1", "tester-2"], configured["pools"]["testers"])

    def test_reject_coordinator_in_collection(self):
        data = team.load_team(team.HERE / "team.json", "configured")
        data["groups"]["engineering"].append("pm")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "team.json"
            path.write_text(json.dumps(data))
            with self.assertRaises(ValueError):
                team.load_team(path, "configured")

    def test_exact_policy_and_workspace(self):
        data = team.load_team(team.HERE / "team.json", "real")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for a in data["agents"]:
                project, policy = team.workspace(root, a, "http://127.0.0.1:19080", ("openai", "example-model"))
                self.assertNotIn(project, policy.parents)
                self.assertTrue((project / "fixture" / "controller.txt").is_file())
                config = json.loads((project / "opencode.json").read_text())
                self.assertEqual("http://127.0.0.1:19080/mcp", config["mcp"]["servers"]["agentiplex"]["url"])
                self.assertFalse(config["mcp"]["servers"]["agentiplex"]["codemode"])
                self.assertEqual(a["id"], config["default_agent"])
                rules = json.loads(policy.read_text())
                self.assertEqual("read-only", rules["base"])
                names = {r["resource"] for r in rules["policies"]}
                self.assertIn("agentiplex_get_state:*", names)
                self.assertIn("agentiplex_put_state:*", names)
                for tool in team.DELEGATE_TOOLS:
                    self.assertEqual(a["id"] == "pm", "agentiplex_" + tool + ":*" in names)
                self.assertFalse(any("shell" in n or "edit" in n for n in names))

    def test_smoke_policy_reduces_tool_surface(self):
        data = team.load_team(team.HERE / "team.json", "configured")
        with tempfile.TemporaryDirectory() as directory:
            for a in data["agents"][:2]:
                project, policy = team.workspace(Path(directory), a, "http://127.0.0.1:19080", ("opencode", "big-pickle"), smoke=True)
                names = {r["resource"] for r in json.loads(policy.read_text())["policies"]}
                self.assertIn("agentiplex_put_state:*", names)
                self.assertNotIn("agentiplex_broadcast:*", names)
                self.assertNotIn("agentiplex_dispatch:*", names)
                self.assertEqual(a["id"] == "pm", "agentiplex_send_message:*" in names)
                self.assertIn("smoke test", (project / "AGENTS.md").read_text())

    def test_terminal_diagnostic_does_not_echo_model_text(self):
        d = {"state": "failed", "result": {"status": {"message": {"parts": [{"text": "ACP prompt timed out"}, {"text": "private"}]}}}}
        self.assertEqual("ACP prompt timed out", team.terminal_diagnostic(d)["gatewayReason"])
        self.assertNotIn("private", json.dumps(team.terminal_diagnostic(d)))

    def test_prompt_timeout_validation(self):
        import argparse
        for value in ("30ms", "120s", "10m", "60m", "2h", "unlimited"):
            self.assertEqual(value, team.valid_prompt_timeout(value))
        for value in ("0s", "29ms", "-1m", "1.5h", "10", "9999999999h", "unlimited "):
            with self.assertRaises(argparse.ArgumentTypeError):
                team.valid_prompt_timeout(value)

    def test_default_model_omits_model_config(self):
        a = team.load_team(team.HERE / "team.json", "configured")["agents"][0]
        with tempfile.TemporaryDirectory() as directory:
            project, _ = team.workspace(Path(directory), a, "http://127.0.0.1:19080", None)
            config = json.loads((project / "opencode.json").read_text())
            self.assertNotIn("model", config)
            self.assertNotIn("providers", config)

    def test_evidence_requires_real_worker_and_state(self):
        e = {"agents": [{"id": "pm", "runtime": "opencode"}, {"id": "architect", "runtime": "opencode"}],
             "root": {"id": "root", "target": "pm", "state": "completed", "result": {"text": "review worker-1"}},
             "deliveries": [{"id": "worker-1", "target": "architect", "state": "completed", "result": {"text": "finding"}}],
             "state": [{"key": "pm", "revision": 1, "value": "plan"}, {"key": "architect", "revision": 2, "value": "finding"}]}
        assessment = team.verify_evidence(e)
        self.assertEqual(["worker-1"], assessment["completedRealWorkerReceipts"])
        self.assertTrue(assessment["exactReceiptCitation"])
        e["agents"][1]["runtime"] = "simulated"
        with self.assertRaises(RuntimeError):
            team.verify_evidence(e)
        e["agents"][1]["runtime"] = "opencode"
        e["state"].pop()
        with self.assertRaises(RuntimeError):
            team.verify_evidence(e)
        e["state"].append({"key": "architect", "revision": 2, "value": "finding"})
        e["root"]["result"] = {"text": "uncited summary"}
        assessment = team.verify_evidence(e)
        self.assertEqual(["worker-1"], assessment["completedRealWorkerReceipts"])
        self.assertFalse(assessment["exactReceiptCitation"])

        # Missing prose citation alone passes, but incomplete real work,
        # fake receipt claims, missing results, or absent persisted state do not.
        e["deliveries"].append({"id": "other", "target": "architect", "state": "accepted"})
        with self.assertRaisesRegex(RuntimeError, "unresolved delivery"):
            team.verify_evidence(e)
        e["deliveries"].pop()
        e["deliveries"][0]["state"] = "failed"
        with self.assertRaisesRegex(RuntimeError, "no completed independent real worker"):
            team.verify_evidence(e)
        e["deliveries"][0]["state"] = "completed"
        e["deliveries"][0].pop("result")
        e["root"]["result"] = {"text": "review worker-1"}
        with self.assertRaisesRegex(RuntimeError, "no completed independent real worker"):
            team.verify_evidence(e)
        e["deliveries"][0]["result"] = {"text": "finding"}
        e["deliveries"].clear()
        with self.assertRaisesRegex(RuntimeError, "no completed independent real worker"):
            team.verify_evidence(e)
        e["deliveries"].append({"id": "worker-1", "target": "architect", "state": "completed", "result": {"text": "finding"}})
        e["state"][0]["revision"] = 0
        with self.assertRaisesRegex(RuntimeError, "missing PM shared-state evidence"):
            team.verify_evidence(e)
        e["state"][0]["revision"] = 1
        e["root"]["state"] = "failed"
        with self.assertRaisesRegex(RuntimeError, "PM did not produce a completed live result"):
            team.verify_evidence(e)


if __name__ == "__main__":
    unittest.main()
