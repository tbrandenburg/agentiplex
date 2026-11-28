import copy
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import verify
import provision_check as team


class VerifyTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.ws = Path(tmp.name)
        for args in (("init", "-q"), ("config", "user.name", "T"), ("config", "user.email", "t@example.invalid")):
            subprocess.run(["git", "-C", str(self.ws), *args], check=True)
        (self.ws / "a.txt").write_text("one\n")
        subprocess.run(["git", "-C", str(self.ws), "add", "."], check=True)
        subprocess.run(["git", "-C", str(self.ws), "commit", "-qm", "x"], check=True)

    def run_cmd(self, code):
        verify.verify(self.ws, [sys.executable, "-c", code], 10)

    def test_given_clean_command_when_run_then_passes(self):
        self.run_cmd("print('ok')")

    def test_given_nonzero_exit_when_run_then_blocked(self):
        with self.assertRaisesRegex(RuntimeError, "exited 3"):
            self.run_cmd("raise SystemExit(3)")

    def test_given_command_dirtying_workspace_when_run_then_blocked(self):
        with self.assertRaisesRegex(RuntimeError, "changed"):
            self.run_cmd("open('a.txt','w').write('two')")

    def test_given_timeout_when_run_then_blocked(self):
        with self.assertRaisesRegex(RuntimeError, "TimeoutExpired"):
            verify.verify(self.ws, [sys.executable, "-c", "import time; time.sleep(5)"], 1)


class TeamEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.objective = "Correct the broken link."
        self.root = {"id": "root", "target": "pm", "state": "completed", "text": self.objective, "result": {"text": "reviewed"}}
        self.deliveries = [self.root, {"id": "dev", "target": "developer", "state": "completed", "result": {"text": "changed"}},
                           {"id": "review", "target": "quality", "state": "completed", "result": {"text": "checked"}}]
        self.state = [{"key": "objective", "value": self.objective, "revision": 1}]
        for role in ("pm", "developer", "quality"):
            self.state.append({"key": "note-" + role + "-fixture", "value": json.dumps({"text": "finding"}), "revision": 1})

    def test_completed_independent_deliveries_and_shared_objective(self):
        self.assertEqual(team.verify_evidence(self.root, self.deliveries, self.state, self.objective),
                         ["developer", "quality"])

    def test_missing_reviewer_altered_objective_failed_worker_and_missing_note(self):
        cases = []
        cases.append((self.deliveries[:-1], self.state))
        changed = copy.deepcopy(self.state)
        changed[0]["value"] = "different objective"
        cases.append((self.deliveries, changed))
        failed = copy.deepcopy(self.deliveries)
        failed[1]["state"] = "failed"
        cases.append((failed, self.state))
        cases.append((self.deliveries, self.state[:-1]))
        for deliveries, state in cases:
            with self.assertRaises(RuntimeError):
                team.verify_evidence(self.root, deliveries, state, self.objective)


class TeamWorkspaceTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.ws = Path(tmp.name)
        for args in (("init", "-q"), ("config", "user.name", "T"), ("config", "user.email", "t@example.invalid")):
            team.git(self.ws, *args)
        team.fixture(self.ws, None)
        team.git(self.ws, "add", ".")
        team.git(self.ws, "commit", "-qm", "fixture")
        self.head = team.git(self.ws, "rev-parse", "HEAD")
        self.branch = team.git(self.ws, "rev-parse", "--abbrev-ref", "HEAD")

    def check(self, scenario):
        team.verify_workspace(self.ws, self.head, self.branch, scenario)

    def test_docs_requires_real_fix_and_preserves_scope(self):
        with self.assertRaises(RuntimeError):
            self.check("docs")
        (self.ws / "README.md").write_text("# Synthetic repository\n\n[Usage](docs/usage.md)\n")
        self.check("docs")
        (self.ws / "unexpected.txt").write_text("outside scope")
        with self.assertRaises(RuntimeError):
            self.check("docs")

    def test_defect_requires_real_correctness_not_only_diff(self):
        (self.ws / "calc.py").write_text("def clamp(value, lower, upper):\n    return lower\n")
        with self.assertRaises(RuntimeError):
            self.check("defect")
        (self.ws / "calc.py").write_text("def clamp(value, lower, upper):\n    return min(upper, max(lower, value))\n")
        self.check("defect")
        team.git(self.ws, "add", "calc.py")
        with self.assertRaises(RuntimeError):
            self.check("defect")


if __name__ == "__main__":
    unittest.main()
