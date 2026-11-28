import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import verify


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


if __name__ == "__main__":
    unittest.main()
