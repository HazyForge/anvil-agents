#!/usr/bin/env python3
"""Offline adapter tests. Set ANVIL_HERMES_NATIVE_CONTRACT=1 in the pinned
Hermes image to exercise its actual cli.main quiet-mode implementation too.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

HELPER = Path(__file__).with_name("anvil-hermes-query")
FIXTURE = r'''
import json, os, sys
class Agent:
    session_id = "native-session"
    reasoning_callback = lambda self, value: print(value)
    def run_conversation(self, *args, **kwargs):
        if self.reasoning_callback is not None:
            self.reasoning_callback("PRIVATE_REASONING_CALLBACK")
        print("PRIVATE_REASONING_DISPLAY")
        os.write(1, b"PRIVATE_FD_OUTPUT\n")
        # Neither messages, tool output nor reasoning_content is a final answer.
        return json.loads(os.environ["FIXTURE_RESULT"])
class HermesCLI:
    def __init__(self, **kwargs):
        self.agent = None
        self.session_id = "native-session"
        self.conversation_history = []
        self._active_agent_route_signature = "route"
    def _claim_active_session(self, *args, **kwargs): return True
    def _ensure_runtime_credentials(self): return os.environ.get("FIXTURE_CREDENTIALS_READY", "1") == "1"
    def _resolve_turn_agent_config(self, query):
        return {"signature":"route", "model":"fixture", "runtime":{}}
    def _init_agent(self, *args, **kwargs):
        self.agent = Agent()
        return True
'''
FAKE_MAIN = r'''
def main(**kwargs):
    with open(os.environ["FIXTURE_KWARGS"], "w") as f: json.dump(kwargs, f)
    instance = HermesCLI()
    if not instance._ensure_runtime_credentials(): raise SystemExit(1)
    instance._init_agent()
    result = instance.agent.run_conversation(user_message=kwargs["query"])
    print(result.get("final_response", ""))
    raise SystemExit(int(os.environ.get("FIXTURE_EXIT", "0")))
'''

class HermesQueryTests(unittest.TestCase):
    def run_helper(self, result, exit_code=0, additional=None, native=False, credentials=True):
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            prompt = directory / "private prompt.txt"
            prompt.write_text("PRIVATE_USER_PROMPT", encoding="utf-8")
            kwargs_path = directory / "kwargs.json"
            env = dict(os.environ, FIXTURE_RESULT=json.dumps(result), FIXTURE_EXIT=str(exit_code), FIXTURE_KWARGS=str(kwargs_path), FIXTURE_CREDENTIALS_READY="1" if credentials else "0")
            env.pop("ANVIL_HERMES_ADDITIONAL_ARGS_JSON", None)
            if additional is not None:
                env["ANVIL_HERMES_ADDITIONAL_ARGS_JSON"] = json.dumps(additional)
            if native:
                # Native CLI routing, quiet result handling and exit semantics;
                # replace only provider execution with deterministic offline data.
                bootstrap = directory / "native_contract.py"
                bootstrap.write_text("import cli, runpy, sys\n" + FIXTURE + "\ncli.HermesCLI = HermesCLI\ncli.CLI_CONFIG = {}\ncli._run_cleanup = lambda: None\ncli._finalize_single_query = lambda instance: None\nsys.argv = [sys.argv[1], sys.argv[2]]\nrunpy.run_path(sys.argv[0], run_name='__main__')\n")
                env["ANVIL_HERMES_ADDITIONAL_ARGS_JSON"] = json.dumps(["--toolsets", "none"])
                command = [sys.executable, str(bootstrap), str(HELPER), str(prompt)]
            else:
                (directory / "cli.py").write_text(FIXTURE + FAKE_MAIN)
                env["PYTHONPATH"] = str(directory)
                command = [sys.executable, str(HELPER), str(prompt)]
            process = subprocess.run(command, env=env, capture_output=True, text=True, timeout=30)
            kwargs = json.loads(kwargs_path.read_text()) if kwargs_path.exists() else None
            self.assertNotIn("PRIVATE_REASONING", process.stdout)
            self.assertNotIn("PRIVATE_FD_OUTPUT", process.stdout)
            self.assertNotIn("PRIVATE_USER_PROMPT", " ".join(command))
            return process, kwargs

    def test_only_native_final_answer_is_framed(self):
        process, kwargs = self.run_helper({"final_response":"Exact **final**\nUnicode: café", "reasoning_content":"PRIVATE_REASONING", "messages":[{"role":"tool","content":"tool value"}]}, additional=["--model","fixture-model","--provider","fixture-provider","--query","ignored"])
        self.assertEqual(process.returncode, 0, process.stderr)
        self.assertEqual(json.loads(process.stdout), {"type":"anvil.hermes.final", "version":1, "role":"assistant", "text":"Exact **final**\nUnicode: café"})
        self.assertEqual(len(process.stdout.splitlines()), 1)
        self.assertEqual(kwargs["query"], "PRIVATE_USER_PROMPT")
        self.assertTrue(kwargs["quiet"])
        self.assertEqual(kwargs["model"], "fixture-model")
        self.assertEqual(kwargs["provider"], "fixture-provider")

    def test_failures_partial_and_missing_final_are_not_answers(self):
        for result, code in [({"final_response":"failure diagnostic","failed":True},0), ({"final_response":"incomplete","partial":True},0), ({"messages":[{"role":"assistant","content":"intermediate"}]},0), ({"final_response":{"reasoning":"private"}},0), ({"final_response":""},0), ({"final_response":"valid but native exit failed"},7)]:
            with self.subTest(result=result, code=code):
                process, _ = self.run_helper(result, exit_code=code)
                self.assertNotEqual(process.returncode, 0)
                self.assertEqual(process.stdout, "")
                if code: self.assertEqual(process.returncode, code)

    def test_provider_setup_failure_has_safe_typed_diagnosis(self):
        process, _ = self.run_helper({}, credentials=False)
        self.assertEqual(process.returncode, 1)
        self.assertEqual(json.loads(process.stdout), {"type":"anvil.hermes.error", "version":1, "code":"provider_configuration_unavailable"})

    @unittest.skipUnless(os.environ.get("ANVIL_HERMES_NATIVE_CONTRACT") == "1", "requires pinned native image")
    def test_pinned_native_quiet_mode_contract(self):
        for result, success in [({"final_response":"NATIVE_FINAL_ONLY", "reasoning_content":"PRIVATE_REASONING"},True), ({"final_response":"not a success", "failed":True},False), ({"final_response":"partial", "partial":True},False)]:
            with self.subTest(result=result):
                process, _ = self.run_helper(result, native=True)
                if success:
                    self.assertEqual(process.returncode, 0, process.stderr)
                    self.assertEqual(json.loads(process.stdout)["text"], "NATIVE_FINAL_ONLY")
                else:
                    self.assertNotEqual(process.returncode, 0)
                    self.assertEqual(process.stdout, "")

    @unittest.skipUnless(os.environ.get("ANVIL_HERMES_NATIVE_CONTRACT") == "1", "requires pinned native image")
    def test_pinned_native_provider_setup_failure(self):
        process, _ = self.run_helper({}, native=True, credentials=False)
        self.assertEqual(process.returncode, 1)
        self.assertEqual(json.loads(process.stdout), {"type":"anvil.hermes.error", "version":1, "code":"provider_configuration_unavailable"})

if __name__ == "__main__":
    unittest.main()
