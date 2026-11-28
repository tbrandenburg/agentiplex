"""Provider-free regression of the real shell runner's CLI boundary.

The fake CLI is an explicit test double; the live workflow independently
exercises the daemon, gateways, actual agent work and executable checks.
"""
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "examples/repository-team/run-contract.sh"


def fake_cli(args):
    path = Path(os.environ["CONTRACT_FAKE_DB"])
    db = json.loads(path.read_text()) if path.exists() else {
        "state": {}, "deliveries": {}, "submits": [], "revision": 0
    }

    def save():
        path.write_text(json.dumps(db))

    def put(key, value):
        db["revision"] += 1
        db["state"][key] = {"value": value, "revision": db["revision"]}
        save()
        return db["state"][key]

    def delivery(key, owner, text=""):
        return {"id": key, "target": owner, "text": text,
                "state": "completed", "result": {"fixture": True}}

    if args[:2] == ["state", "get"]:
        print(json.dumps(db["state"][args[3]]))
    elif args[:2] == ["state", "put"]:
        key, value, flag, revision = args[3:]
        assert flag == "--revision"
        old = db["state"].get(key, {}).get("revision", 0)
        if int(revision) != old:
            return 1
        print(json.dumps(put(key, value)))
    elif args[0] == "send":
        _, owner, text, flag, key = args
        assert flag == "--idempotency-key" and owner == "pm"
        db["submits"].append(key)
        if key in db["deliveries"]:
            assert db["deliveries"][key]["text"] == text
        else:
            db["deliveries"][key] = delivery(key, owner, text)
        save()
        print(json.dumps(db["deliveries"][key]))
    elif args[:2] == ["messages", "status"]:
        print(json.dumps(db["deliveries"][args[2]]))
    elif args[:2] == ["messages", "wait"]:
        key = args[2]
        contract = json.loads(db["state"]["contract"]["value"])

        def verify(index, receipt, evidence):
            criterion = contract["criteria"][index]
            db["deliveries"][receipt] = delivery(receipt, criterion["owner"])
            criterion.update(status="verified", delivery=receipt, evidence=evidence)

        if key == "contract-round-1":
            verify(0, "test-baseline", "original test exit 1")
            contract["criteria"][2].update(status="rework", evidence="failed original test")
            put("contract", json.dumps(contract))
            # Simulate lost wait response after checkpoint persisted.
            if not db.get("interrupted"):
                db["interrupted"] = True
                save()
                return 1
        elif os.environ.get("CONTRACT_FAKE_STALL") != "1":
            verify(1, "repair", "fixed candidate")
            verify(2, "test-repaired", "original test exit 0")
            verify(3, "review", "scope accepted")
            put("contract", json.dumps(contract))
        print(json.dumps(db["deliveries"][key]))
    else:
        raise AssertionError(args)
    return 0


class RunnerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.db = root / "state.json"
        cli = root / "agentiplex"
        cli.write_text("#!/bin/sh\nexec " + shlex.quote(sys.executable) + " "
                       + shlex.quote(str(Path(__file__).resolve())) + ' "$@"\n')
        cli.chmod(0o700)
        self.env = dict(os.environ, AGENTIPLEX_BIN=str(cli),
                        CONTRACT_FAKE_DB=str(self.db))
        self.invoke("init")

    def invoke(self, mode, ok=True):
        result = subprocess.run(["bash", str(RUNNER), mode], env=self.env,
                                text=True, capture_output=True, timeout=60)
        self.assertEqual(result.returncode == 0, ok, result.stdout + result.stderr)
        return result.stdout + result.stderr

    def state(self):
        return json.loads(self.db.read_text())

    def test_interrupted_wait_reuses_intent_and_closed_run_is_noop(self):
        self.assertIn("unresolved", self.invoke("run", ok=False))
        self.assertEqual(self.state()["submits"], ["contract-round-1"])
        self.assertIn("rework", self.invoke("run"))
        db = self.state()
        self.assertEqual(db["submits"], ["contract-round-1", "contract-round-1",
                                         "contract-round-2"])
        self.assertEqual(json.loads(db["state"]["contract-run"]["value"])["round"], 0)
        self.invoke("run")
        self.assertEqual(self.state()["submits"], db["submits"])

    def test_tampered_definition_refused_before_dispatch(self):
        db = self.state()
        value = json.loads(db["state"]["contract"]["value"])
        value["criteria"].pop()
        db["state"]["contract"]["value"] = json.dumps(value)
        self.db.write_text(json.dumps(db))
        self.assertIn("approved criteria changed", self.invoke("run", ok=False))
        self.assertEqual(self.state()["submits"], [])

    def test_blocker_refused_before_dispatch(self):
        db = self.state()
        value = json.loads(db["state"]["contract"]["value"])
        value["blocker"] = "human decision required"
        db["state"]["contract"]["value"] = json.dumps(value)
        self.db.write_text(json.dumps(db))
        self.assertIn("human decision required", self.invoke("run", ok=False))
        self.assertEqual(self.state()["submits"], [])

    def test_budget_exhaustion_is_not_fulfillment(self):
        self.env["CONTRACT_FAKE_STALL"] = "1"
        self.invoke("run", ok=False)
        self.assertIn("round budget exhausted", self.invoke("run", ok=False))
        db = self.state()
        self.assertEqual(json.loads(db["state"]["contract-run"]["value"])["round"], 9)
        self.assertNotEqual(json.loads(db["state"]["contract"]["value"])["criteria"][2]["status"], "verified")


if __name__ == "__main__":
    if os.environ.get("CONTRACT_FAKE_DB"):
        sys.exit(fake_cli(sys.argv[1:]))
    unittest.main()
