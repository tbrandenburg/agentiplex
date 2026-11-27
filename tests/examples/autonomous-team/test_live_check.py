import copy
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import live_check

ROOT = {"id": "root", "target": "pm", "state": "completed", "result": {"text": "review"}}
WORKER = {"id": "w1", "target": "architect", "state": "completed", "result": {"text": "finding"}}
STATE = [{"key": "pm", "revision": 1, "value": "plan"}, {"key": "architect", "revision": 2, "value": "finding"}]


class VerifyEvidenceTests(unittest.TestCase):
    def check(self, root=ROOT, deliveries=(WORKER,), state=STATE):
        return live_check.verify_evidence(copy.deepcopy(root), copy.deepcopy(list(deliveries)), copy.deepcopy(state))

    def test_given_real_worker_and_state_when_verified_then_receipts_returned(self):
        self.assertEqual(["w1"], self.check(deliveries=[ROOT, WORKER]))

    def test_given_note_key_of_real_worker_then_accepted_but_other_roles_note_is_not(self):
        note = lambda k: [STATE[0], {"key": k, "revision": 1, "value": "x"}]
        self.check(state=note("note-architect-0179-ab"))
        with self.assertRaisesRegex(RuntimeError, "worker shared-state"):
            self.check(state=note("note-tester-1-0179-ab"))

    def test_given_failed_or_missing_work_when_verified_then_blocked(self):
        with self.assertRaisesRegex(RuntimeError, "unresolved"):
            self.check(deliveries=[WORKER, dict(WORKER, id="w2", state="accepted")])
        with self.assertRaisesRegex(RuntimeError, "no completed independent"):
            self.check(deliveries=[])
        with self.assertRaisesRegex(RuntimeError, "no completed independent"):
            self.check(deliveries=[{k: v for k, v in WORKER.items() if k != "result"}])
        with self.assertRaisesRegex(RuntimeError, "PM did not"):
            self.check(root=dict(ROOT, state="failed"))
        with self.assertRaisesRegex(RuntimeError, "PM shared-state"):
            self.check(state=[dict(STATE[0], revision=0), STATE[1]])


if __name__ == "__main__":
    unittest.main()
