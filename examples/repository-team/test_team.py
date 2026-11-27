import json
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import repo
import team
import teamlib
import verify


class RepositoryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.workspace = self.root / "repo"
        self.workspace.mkdir()
        self.endpoint = "http://127.0.0.1:19080"
        self.ms = 600000
        self.config = {
            "model": {"providerID": "opencode", "model": "big-pickle"},
            "mcp": repo.mcp_config(self.endpoint, self.ms),
        }
        (self.workspace / "opencode.json").write_text(json.dumps(self.config))
        (self.workspace / "README.md").write_text("before\n")
        for args in (("init", "-q"), ("config", "user.name", "Test"),
                     ("config", "user.email", "test@example.invalid"), ("add", "."),
                     ("commit", "-qm", "initial")):
            subprocess.run(["git", "-C", str(self.workspace), *args], check=True)

    def test_explicit_clean_workspace_and_unchanged_config(self):
        path, before, model = repo.inspect_workspace(str(self.workspace), self.endpoint, self.ms)
        self.assertEqual(self.workspace.resolve(), path)
        self.assertEqual(self.config["model"], model)
        self.assertFalse(before["status"])
        self.assertEqual(self.config, json.loads((self.workspace / "opencode.json").read_text()))
        with self.assertRaises(ValueError):
            repo.inspect_workspace(str(self.workspace), "http://127.0.0.1:19081", self.ms)
        (self.workspace / "README.md").write_text("dirty\n")
        with self.assertRaisesRegex(ValueError, "clean"):
            repo.inspect_workspace(str(self.workspace), self.endpoint, self.ms)

    def test_log_tail_shows_last_lines_truncated_and_tolerates_missing_log(self):
        log = self.root / "pm.log"
        log.write_text("one\n\ntwo\nthree\n" + "x" * 500 + "\nlast\n")
        tail = team.log_tail(log, lines=2, width=10)
        self.assertEqual("\n  log: " + "x" * 10 + "\n  log: last", tail)
        self.assertNotIn("three", tail)
        self.assertEqual("", team.log_tail(self.root / "missing.log"))

    def test_model_is_optional_and_never_invented(self):
        del self.config["model"]
        (self.workspace / "opencode.json").write_text(json.dumps(self.config))
        subprocess.run(["git", "-C", str(self.workspace), "commit", "-qam", "no model"], check=True)
        _, _, model = repo.inspect_workspace(str(self.workspace), self.endpoint, self.ms)
        self.assertIsNone(model)
        self.config["model"] = {"providerID": "opencode"}
        (self.workspace / "opencode.json").write_text(json.dumps(self.config))
        subprocess.run(["git", "-C", str(self.workspace), "commit", "-qam", "bad model"], check=True)
        with self.assertRaisesRegex(ValueError, "model must be"):
            repo.inspect_workspace(str(self.workspace), self.endpoint, self.ms)

    def test_clone_config_without_model_omits_key(self):
        subprocess.run(["git", "-C", str(self.workspace), "rm", "-q", "opencode.json"], check=True)
        subprocess.run(["git", "-C", str(self.workspace), "commit", "-qm", "rm"], check=True)
        repo.prepare_clone_config(self.workspace, self.endpoint, None, self.ms)
        written = json.loads((self.workspace / "opencode.json").read_text())
        self.assertNotIn("model", written)
        self.assertEqual(self.config["mcp"], written["mcp"])
        self.assertFalse(repo.git_snapshot(self.workspace)["status"])

    def test_concurrent_independent_clones_and_clean_worktrees(self):
        # Separate source with no OpenCode config, branch/tag and an uncommitted edit.
        source = self.workspace
        subprocess.run(["git", "-C", str(source), "rm", "opencode.json"], check=True)
        subprocess.run(["git", "-C", str(source), "commit", "-qm", "remove config"], check=True)
        subprocess.run(["git", "-C", str(source), "tag", "demo-tag"], check=True)
        (source / "README.md").write_text("operator dirty change\n")
        original = repo.git_snapshot(source)
        roots = [self.root / f"run-{i}" for i in range(2)]
        for root in roots:
            root.mkdir()
        with ThreadPoolExecutor(max_workers=2) as executor:
            workspaces = list(executor.map(lambda root: repo.clone_workspace(root, str(source), "demo-tag"), roots))
        self.assertNotEqual(workspaces[0], workspaces[1])
        for workspace, root in zip(workspaces, roots):
            self.assertEqual(root / "workspace", workspace)
            self.assertTrue((workspace / ".git").is_file())
            self.assertEqual("HEAD", repo.git_snapshot(workspace)["branch"])
            self.assertEqual(original["head"], repo.git_snapshot(workspace)["head"])
            self.assertFalse((root / "repository.git" / "objects" / "info" / "alternates").exists())
            repo.prepare_clone_config(workspace, self.endpoint, self.config["model"], self.ms)
            self.assertFalse(repo.inspect_workspace(workspace, self.endpoint, self.ms)[1]["status"])
            with self.assertRaisesRegex(ValueError, "already has"):
                repo.prepare_clone_config(workspace, self.endpoint, self.config["model"], self.ms)
        (workspaces[0] / "README.md").write_text("first run only\n")
        self.assertEqual("before\n", (workspaces[1] / "README.md").read_text())
        self.assertEqual(original, repo.git_snapshot(source))
        with repo.directory_lock(workspaces[0]):
            with self.assertRaisesRegex(ValueError, "already in use"):
                with repo.directory_lock(workspaces[0]):
                    pass
            with repo.directory_lock(workspaces[1]):
                pass
        with repo.directory_lock(workspaces[0]):
            pass

    def test_clone_rejects_unsafe_inputs_and_existing_config(self):
        for url in ("ext::sh -c bad", "https://user:secret@example.com/repo", "-option",
                    "https://example.com/repo?token=secret"):
            with self.assertRaises(ValueError):
                repo.source_url(url)
        root = self.root / "clone"
        root.mkdir()
        with self.assertRaises(ValueError):
            repo.clone_workspace(root, str(self.workspace), "-bad")
        workspace = repo.clone_workspace(root, str(self.workspace))
        with self.assertRaisesRegex(ValueError, "already has"):
            repo.prepare_clone_config(workspace, self.endpoint, self.config["model"], self.ms)
        self.assertEqual(self.config, json.loads((workspace / "opencode.json").read_text()))
        self.assertFalse(repo.git_snapshot(workspace)["status"])

    def test_team_is_data_and_grants_derive_from_it(self):
        path = Path(team.__file__).with_name("team.json")
        agents = team.load_roster(path)
        self.assertLessEqual(len(path.read_text().splitlines()), 10)
        self.assertEqual("pm", agents[0]["id"])
        args = SimpleNamespace(gateway="opencode-a2a-lite", prompt_timeout="10m", pass_env=["EXAMPLE_API_KEY"], accept_full_access=True)
        for a in agents:
            profile, rules = team.policy_for(agents, a)
            self.assertEqual(a["access"], profile)
            self.assertEqual(profile, rules["base"])
            resources = {p["resource"] for p in rules["policies"]}
            for tool in team.READ_TOOLS:
                self.assertIn("agentiplex_" + tool + ":*", resources)
            for tool in ("ask", "note", "read_notes"):
                self.assertEqual(a is agents[0] or tool != "ask", "agentiplex_" + tool + ":*" in resources)
            for tool in team.DELEGATE_TOOLS:
                self.assertEqual(a is agents[0], "agentiplex_" + tool + ":*" in resources)
            cmd = team.gateway_command(args, self.workspace, self.root / "policy.json", profile)
            self.assertEqual(str(self.workspace), cmd[cmd.index("--workspace") + 1])
            self.assertEqual("10m", cmd[cmd.index("--prompt-timeout") + 1])
            self.assertEqual("danger-full-access" if profile == "full-access" else "read", cmd[cmd.index("--access") + 1])
            self.assertEqual(["--pass-env", "EXAMPLE_API_KEY"], cmd[-2:])
        self.assertEqual({"pm": "read-only", "architect": "read-only", "developer": "full-access",
                          "tester": "full-access", "quality": "read-only"}, {a["id"]: a["access"] for a in agents})

    def test_adding_an_agent_needs_no_python(self):
        path = self.root / "team.json"
        data = {"agents": json.loads(Path(team.__file__).with_name("team.json").read_text())["agents"]}
        data["agents"].append({"id": "docs-writer", "access": "read-only", "role": "Check documentation."})
        path.write_text(json.dumps(data))
        agents = team.load_roster(path)
        self.assertIn("docs-writer: Check documentation.", team.pm_prompt(agents, "Do it"))
        _, rules = team.policy_for(agents, agents[-1])
        self.assertNotIn("agentiplex_ask:*", {p["resource"] for p in rules["policies"]})
        for bad in ({"id": "x", "access": "root", "role": "r"}, {"id": "pm", "access": "read-only", "role": "dup"},
                    {"id": "y", "access": "read-only"}, {"id": "z", "access": "read-only", "role": " "}):
            path.write_text(json.dumps({"agents": data["agents"] + [bad]}))
            with self.assertRaises(ValueError):
                team.load_roster(path)

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

    def test_timeouts_must_fit_and_are_launcher_options(self):
        script = str(Path(team.__file__))
        base = [sys.executable, "-B", script, "--workspace", "/nonexistent", "--accept-full-access", "--check"]
        run = [a for a in base if a != "--check"] + ["--seconds", "600"]
        result = subprocess.run(run, capture_output=True, text=True)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("must exceed --prompt-timeout", result.stderr)
        result = subprocess.run(base + ["--seconds", "180"], capture_output=True, text=True)
        self.assertNotIn("must exceed --prompt-timeout", result.stderr)
        for extra, message in (
                               (["--mcp-timeout", "unlimited"], "finite duration"),
                               (["--restart-daemon", "--repo", "/nonexistent"], "not allowed")):
            result = subprocess.run(base + extra, capture_output=True, text=True)
            self.assertNotEqual(0, result.returncode)
            self.assertIn(message, result.stderr)

    def test_start_registers_only_endpoints_with_same_workspace(self):
        args = SimpleNamespace(port=19080, headless=True, gateway="gateway", prompt_timeout="10m",
                               pass_env=[], workspace=self.workspace, accept_full_access=True)
        agents = team.load_roster(Path(team.__file__).with_name("team.json"))
        runner = team.RepositoryTeam(args, {"agents": agents}, self.root, float("inf"))
        commands, calls = [], []

        def spawn(name, cmd, env, cwd=None, append=False):
            commands.append((name, cmd, env, cwd))
            if name == "daemon":
                (self.root / "daemon.log").write_text("INFO agentiplexd ready listen=127.0.0.1:19080 broker_mode=embedded\n")
            return name

        def cli(*args):
            calls.append(args)
            if args == ("agents", "list"):
                return [{"id": a["id"]} for a in agents]

        def card(url, proc, name, skill):
            self.assertEqual(name, skill)
            return {"skills": [{"tags": [skill, "real-opencode"]}]}

        with patch.object(runner, "spawn", side_effect=spawn), patch.object(runner, "cli", side_effect=cli), \
             patch.object(runner, "gateway_ready", side_effect=lambda n, p: "http://127.0.0.1:200" + str(len(commands))), \
             patch.object(runner, "wait_card", side_effect=card), \
             patch.object(teamlib, "get", side_effect=lambda url: {"ready": True} if url.endswith("/readyz") else []):
            runner.start()
        self.assertEqual(len(agents) + 1, len(commands))
        self.assertNotIn(str(self.workspace), commands[0][1])
        for name, cmd, env, cwd in commands[1:]:
            self.assertEqual(self.workspace, cwd)
            self.assertEqual(str(self.workspace), cmd[cmd.index("--workspace") + 1])
            self.assertEqual(name, env["A2A_NAME"])
        registrations = [c for c in calls if c[0] == "register"]
        self.assertEqual(len(agents), len(registrations))
        self.assertTrue(all(len(c) == 3 and c[2].startswith("http://127.0.0.1:") for c in registrations))
        self.assertFalse(repo.git_snapshot(self.workspace)["status"])

    def test_daemon_auto_port_uses_owned_readiness_record(self):
        args = SimpleNamespace(port=0, headless=True, accept_full_access=True)
        runner = team.RepositoryTeam(args, {}, self.root, float("inf"))
        (self.root / "daemon.log").write_text("INFO agentiplexd ready listen=127.0.0.1:29451 broker_mode=embedded\n")
        with patch.object(runner, "spawn") as spawn, patch.object(teamlib, "get", return_value={"ready": True}) as get:
            runner.start_daemon("repository-demo", False)
            cmd = spawn.call_args.args[1]
            self.assertEqual("127.0.0.1:0", cmd[cmd.index("--listen") + 1])
            self.assertNotIn("--public-url", cmd)
            get.assert_called_once_with("http://127.0.0.1:29451/readyz")
        self.assertEqual("http://127.0.0.1:29451", runner.endpoint)
        args.port = 19080
        with patch.object(runner, "spawn"), patch.object(teamlib, "get") as get:
            with self.assertRaisesRegex(RuntimeError, "unexpected daemon"):
                runner.start_daemon("repository-demo", False)
            get.assert_not_called()

    def test_daemon_restart_only_when_asked(self):
        class Dead:
            def poll(self):
                return -9
        args = SimpleNamespace(port=19080, restart_daemon=False)
        runner = team.RepositoryTeam(args, {}, self.root, float("inf"))
        runner.children = [("daemon", Dead())]
        with self.assertRaisesRegex(RuntimeError, "daemon exited"):
            runner.alive()
        args.restart_daemon = True
        runner.daemon_spec = (["agentiplexd"], {})
        with patch.object(runner, "spawn") as spawn, patch.object(runner, "wait_daemon") as wait:
            runner.alive()
        spawn.assert_called_once_with("daemon", ["agentiplexd"], {}, append=True)
        wait.assert_called_once_with()
        self.assertEqual(1, runner.daemon_restarts)

    def evidence(self):
        roster = team.load_roster(Path(team.__file__).with_name("team.json"))
        root = {"id": "root", "target": "pm", "state": "completed", "result": {"text": "done"}}
        workers = [{"id": role, "target": role, "state": "completed", "result": {"text": "finding"},
                    "returnedAt": "2026-11-27T10:00:00Z"} for role in ("architect", "developer", "tester", "quality")]
        deliveries = [root, *workers]
        return {"root": root, "deliveries": deliveries, "agents": roster,
                "toolCalls": teamlib.call_evidence(deliveries, "pm"),
                "state": [{"key": "note-team-1-aa", "revision": 1, "value": "{}"}]}

    def test_acceptance_requires_every_role_ask_evidence_and_notes(self):
        e = self.evidence()
        self.assertEqual(["architect", "developer", "quality", "tester"], team.verify_collaboration(e))
        self.assertEqual(4, e["toolCalls"]["askCalls"])
        self.assertEqual(1, e["toolCalls"]["coordinatorTurns"])
        e["deliveries"][-1]["state"] = "accepted"
        with self.assertRaisesRegex(RuntimeError, "did not complete"):
            team.verify_collaboration(e)
        for role in ("quality", "tester"):
            e = self.evidence()
            e["deliveries"] = [d for d in e["deliveries"] if d["id"] != role]
            with self.assertRaisesRegex(RuntimeError, "missing completed worker results: " + role):
                team.verify_collaboration(e)
        e = self.evidence()
        e["deliveries"][-1].pop("result")
        with self.assertRaisesRegex(RuntimeError, "missing completed worker results: quality"):
            team.verify_collaboration(e)
        e = self.evidence()
        e["root"]["state"] = "failed"
        with self.assertRaisesRegex(RuntimeError, "PM did not return"):
            team.verify_collaboration(e)
        e = self.evidence()
        e["state"] = []
        with self.assertRaisesRegex(RuntimeError, "shared notes"):
            team.verify_collaboration(e)
        e = self.evidence()
        for d in e["deliveries"]:
            d.pop("returnedAt", None)
        e["toolCalls"] = teamlib.call_evidence(e["deliveries"], "pm")
        self.assertEqual(4, e["toolCalls"]["otherDelegations"])
        with self.assertRaisesRegex(RuntimeError, "PM called ask"):
            team.verify_collaboration(e)

    def test_prompt_is_generic_and_bounded(self):
        agents = team.load_roster(Path(team.__file__).with_name("team.json"))
        prompt = team.pm_prompt(agents, "Improve README")
        self.assertIn("Improve README", prompt)
        for a in agents[1:]:
            self.assertIn("- " + a["id"] + ": " + a["role"], prompt)
        self.assertIn("note", prompt)
        self.assertIn("commits", prompt)
        for mechanics in ("idempotency", "receipt", "poll", "revision", "resubmit", "key", "six"):
            self.assertNotIn(mechanics, prompt.lower())
        with self.assertRaises(ValueError):
            team.pm_prompt(agents, "x" * 4096)

    def test_git_evidence_and_operator_verification(self):
        before = repo.git_snapshot(self.workspace)
        (self.workspace / "README.md").write_text("after\n")
        evidence = self.root / "evidence"
        evidence.mkdir()
        metadata = repo.save_workspace_evidence(self.workspace, before, evidence, self.config["model"])
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
            repo.save_workspace_evidence(self.workspace, before, evidence, self.config["model"])


if __name__ == "__main__":
    unittest.main()
