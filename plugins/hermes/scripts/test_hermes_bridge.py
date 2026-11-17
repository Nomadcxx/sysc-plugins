"""Tests for the bundled bridge's local changes (Task 3a).

Stdlib unittest only. Run: python3 -m unittest test_hermes_bridge (from this
directory), or through go test ./plugins/hermes. The token is a fake.
"""
from __future__ import annotations

import importlib.util
import json
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import types
import unittest
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
BRIDGE_PATH = HERE / "hermes_bridge.py"
FAKE_TOKEN = "fake-token"

_spec = importlib.util.spec_from_file_location("hermes_bridge_under_test", BRIDGE_PATH)
bridge = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bridge)


class FakeRpc:
    """Records every dispatch; answers from a per-method table."""

    def __init__(self) -> None:
        self.calls: list[tuple[str, dict]] = []
        self.responses: dict[str, dict] = {}

    def dispatch(self, method: str, params: dict) -> dict:
        self.calls.append((method, dict(params)))
        return self.responses.get(method, {"result": {"status": "accepted"}})

    def calls_to(self, method: str) -> list[dict]:
        return [params for name, params in self.calls if name == method]


class BridgeCase(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        # The stock snapshot reads <hermes_home>/config.yaml and fails without it.
        (self.tmp / "home").mkdir()
        (self.tmp / "home" / "config.yaml").write_text("model:\n  default: m0\n")
        self.state = bridge.HermesState(self.tmp / "state.json", self.tmp / "home")
        self.state._state["session"]["id"] = "sid-1"
        self.rpc = FakeRpc()
        self.httpd = bridge.create_server("127.0.0.1", 0, self.state, self.rpc, "hermes")
        self.httpd.bridge_token = FAKE_TOKEN
        thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(self.httpd.server_close)
        self.addCleanup(self.httpd.shutdown)
        self.base = "http://127.0.0.1:%d" % self.httpd.server_address[1]

    def request(self, method: str, path: str, body: dict | None = None) -> tuple[int, dict]:
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method,
                                     headers={"X-Bridge-Token": FAKE_TOKEN, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=10) as resp:
                return resp.status, json.loads(resp.read() or b"{}")
        except urllib.error.HTTPError as err:
            with err:
                return err.code, json.loads(err.read() or b"{}")

    def post(self, path: str, body: dict) -> tuple[int, dict]:
        return self.request("POST", path, body)


class ApprovalIdentityTest(BridgeCase):
    def setUp(self) -> None:
        super().setUp()
        self.install_resolver(supports_request_id=True)

    def install_resolver(self, supports_request_id: bool) -> None:
        """Stand in for Hermes' tools.approval so the handler can see whether
        the resolver takes request_id."""
        tools = types.ModuleType("tools")
        approval = types.ModuleType("tools.approval")
        if supports_request_id:
            def resolve_gateway_approval(session_key, choice, resolve_all=False, reason=None, request_id=None):
                return 1
        else:
            def resolve_gateway_approval(session_key, choice, resolve_all=False):
                return 1
        approval.resolve_gateway_approval = resolve_gateway_approval
        tools.approval = approval
        saved = {name: sys.modules.get(name) for name in ("tools", "tools.approval")}

        def restore() -> None:
            for name, module in saved.items():
                if module is None:
                    sys.modules.pop(name, None)
                else:
                    sys.modules[name] = module

        self.addCleanup(restore)
        sys.modules["tools"] = tools
        sys.modules["tools.approval"] = approval

    def show_approval(self, request_id: str) -> None:
        self.state.apply_event({"params": {"type": "approval.request", "payload": {
            "command": "rm -rf build", "description": "delete build", "tool_name": "terminal",
            "request_id": request_id,
        }}})

    def test_state_publishes_the_pending_request_id(self) -> None:
        self.show_approval("req-1")
        status, state = self.request("GET", "/state")
        self.assertEqual(status, 200)
        self.assertTrue(state["approval"]["pending"])
        self.assertEqual(state["approval"]["request_id"], "req-1")

    def test_a_request_id_that_is_not_the_shown_one_resolves_nothing(self) -> None:
        self.show_approval("req-1")
        status, body = self.post("/approval", {"choice": "once", "request_id": "req-other"})
        self.assertEqual(status, 409)
        self.assertEqual(body.get("error"), "approval_mismatch")
        self.assertEqual(self.rpc.calls_to("approval.respond"), [])
        self.assertTrue(self.state.snapshot()["approval"]["pending"])

    def test_a_request_id_with_nothing_pending_resolves_nothing(self) -> None:
        status, body = self.post("/approval", {"choice": "once", "request_id": "req-1"})
        self.assertEqual(status, 409)
        self.assertEqual(body.get("error"), "approval_mismatch")
        self.assertEqual(self.rpc.calls_to("approval.respond"), [])

    def test_the_matching_request_id_is_forwarded_to_a_resolver_that_takes_it(self) -> None:
        self.show_approval("req-1")
        self.rpc.responses["approval.respond"] = {"result": {"resolved": 1}}
        status, body = self.post("/approval", {"choice": "once", "request_id": "req-1"})
        self.assertEqual(status, 200)
        self.assertEqual(self.rpc.calls_to("approval.respond"),
                         [{"session_id": "sid-1", "choice": "once", "all": False, "request_id": "req-1"}])
        self.assertFalse(body["state"]["approval"]["pending"])

    def test_a_forwarded_id_the_resolver_no_longer_has_answers_409(self) -> None:
        self.show_approval("req-1")
        self.rpc.responses["approval.respond"] = {"result": {"resolved": 0}}
        status, body = self.post("/approval", {"choice": "once", "request_id": "req-1"})
        self.assertEqual(status, 409)
        self.assertEqual(body.get("error"), "approval_mismatch")

    def test_without_resolver_support_only_the_shown_request_is_resolved(self) -> None:
        self.install_resolver(supports_request_id=False)
        self.show_approval("req-1")
        status, _ = self.post("/approval", {"choice": "once", "request_id": "req-other"})
        self.assertEqual(status, 409)
        self.assertEqual(self.rpc.calls_to("approval.respond"), [])
        status, body = self.post("/approval", {"choice": "deny", "request_id": "req-1"})
        self.assertEqual(status, 200)
        # The stock call: this resolver resolves the oldest, which is the shown one.
        self.assertEqual(self.rpc.calls_to("approval.respond"),
                         [{"session_id": "sid-1", "choice": "deny", "all": False}])
        self.assertFalse(body["state"]["approval"]["pending"])

    def test_no_request_id_keeps_the_stock_behavior(self) -> None:
        self.show_approval("req-1")
        status, body = self.post("/approval", {"choice": "session", "all": True})
        self.assertEqual(status, 200)
        self.assertEqual(self.rpc.calls_to("approval.respond"),
                         [{"session_id": "sid-1", "choice": "session", "all": True}])
        self.assertFalse(body["state"]["approval"]["pending"])
        # Stock also answers with nothing pending.
        status, _ = self.post("/approval", {})
        self.assertEqual(status, 200)
        self.assertEqual(self.rpc.calls_to("approval.respond")[-1],
                         {"session_id": "sid-1", "choice": "deny", "all": False})


class ServerRequestApprovalTest(ApprovalIdentityTest):
    """Hermes b200f5b sends an approval as an ``approval`` server->client
    request (server_requests.send_async), not an ``approval.request`` event.
    The frame reaches the bridge through StateTransport.write."""

    def server_request(self, request_id: str, frame_id: str = "srv-1", nested: bool = False) -> dict:
        payload = {"command": "rm -rf build", "description": "delete build", "request_id": request_id,
                   "choices": ["once", "session", "always", "deny"]}
        params = {"session_id": "sid-1", "payload": payload} if nested else {"session_id": "sid-1", **payload}
        return {"jsonrpc": "2.0", "id": frame_id, "method": "approval", "params": params}

    def test_a_server_request_shows_the_card(self) -> None:
        for nested in (False, True):
            with self.subTest(nested=nested):
                self.state.clear_approval()
                bridge.StateTransport(self.state).write(self.server_request("req-9", nested=nested))
                approval = self.state.snapshot()["approval"]
                self.assertTrue(approval["pending"])
                self.assertEqual(approval["request_id"], "req-9")
                self.assertEqual(approval["request"]["command"], "rm -rf build")
                self.assertNotIn("session_id", approval["request"])
                self.assertEqual(self.state.snapshot()["hermes"]["status"], "attention")

    def test_a_server_request_card_resolves_by_request_id(self) -> None:
        self.state.apply_event(self.server_request("req-9"))
        self.rpc.responses["approval.respond"] = {"result": {"resolved": 1}}
        status, body = self.post("/approval", {"choice": "once", "request_id": "req-9"})
        self.assertEqual(status, 200)
        self.assertEqual(self.rpc.calls_to("approval.respond"),
                         [{"session_id": "sid-1", "choice": "once", "all": False, "request_id": "req-9"}])
        self.assertFalse(body["state"]["approval"]["pending"])

    def test_the_event_form_still_works(self) -> None:
        self.show_approval("req-1")
        self.assertEqual(self.state.snapshot()["approval"]["request_id"], "req-1")

    def test_request_cancel_withdraws_only_its_own_card(self) -> None:
        self.state.apply_event(self.server_request("req-9", frame_id="srv-1"))
        self.state.apply_event({"jsonrpc": "2.0", "method": "event", "params": {
            "type": "request.cancel", "session_id": "sid-1",
            "payload": {"id": "srv-other", "method": "approval", "reason": "resolved"}}})
        self.assertTrue(self.state.snapshot()["approval"]["pending"])
        self.state.apply_event({"jsonrpc": "2.0", "method": "event", "params": {
            "type": "request.cancel", "session_id": "sid-1",
            "payload": {"id": "srv-1", "method": "approval", "reason": "timeout"}}})
        self.assertFalse(self.state.snapshot()["approval"]["pending"])

    def test_request_cancel_leaves_an_event_card_alone(self) -> None:
        self.show_approval("req-1")
        self.state.apply_event({"jsonrpc": "2.0", "method": "event", "params": {
            "type": "request.cancel", "session_id": "sid-1",
            "payload": {"id": "srv-1", "method": "approval", "reason": "resolved"}}})
        self.assertTrue(self.state.snapshot()["approval"]["pending"])

    def test_other_server_requests_do_not_show_a_card(self) -> None:
        self.state.apply_event({"jsonrpc": "2.0", "id": "srv-2", "method": "setup_beat",
                                "params": {"session_id": "sid-1", "request_id": "x"}})
        self.assertFalse(self.state.snapshot()["approval"]["pending"])


class ModelNameTest(BridgeCase):
    def test_flag_like_or_spaced_names_are_refused_before_config_set(self) -> None:
        for body in (
            {"model": "gpt 4 --global"},
            {"model": "-x"},
            {"model": "a\tb"},
            {"model": "gpt-5", "provider": "p --global"},
            {"model": "gpt-5", "provider": "-p"},
            {"model": "   "},
            {"model": ""},
            {"model": "gpt-5", "provider": "   "},
        ):
            with self.subTest(body=body):
                status, reply = self.post("/model", body)
                self.assertEqual(status, 400)
                self.assertEqual(reply.get("error"), "invalid_model")
        self.assertEqual(self.rpc.calls_to("config.set"), [])
        self.assertEqual(self.state.snapshot()["hermes"]["model"], bridge.default_state()["hermes"]["model"])

    def test_valid_names_build_the_stock_value(self) -> None:
        cases = [
            ({"model": "openai/gpt-5.1"}, "openai/gpt-5.1"),
            ({"model": " openai/gpt-5.1 ", "provider": ""}, "openai/gpt-5.1"),
            ({"model": "gpt-5.1", "provider": "openai"}, "gpt-5.1 --provider openai"),
            ({"model": "gpt-5.1", "provider": "openai", "persist": True}, "gpt-5.1 --provider openai --global"),
        ]
        for body, value in cases:
            with self.subTest(body=body):
                status, _ = self.post("/model", body)
                self.assertEqual(status, 200)
                self.assertEqual(self.rpc.calls_to("config.set")[-1],
                                 {"key": "model", "value": value, "session_id": "sid-1"})


# Functions the local changes may touch in the Task 3 copy (commit a8384bd):
# Task 3a's approval and model handlers, and _send_json, which keeps a client
# hang-up from being recorded as a Hermes failure.
ALLOWED = {"_set_approval", "_handle_approval", "_handle_model", "apply_event", "_send_json"}
TASK3_COMMIT = "a8384bdeeff6d2f65b8ccbe475ed89fed2cd2329"


def _function_at(lines: list[str], index: int) -> str:
    """The method or top-level function enclosing line index (0-based), or
    '' outside any. Nested helpers count as part of their method."""
    for i in range(min(index, len(lines) - 1), -1, -1):
        m = re.match(r"( {0,4})def (\w+)\(", lines[i])
        if m:
            return m.group(2)
        if lines[i] and not lines[i][0].isspace() and not lines[i].startswith(("#", ")")):
            return ""
    return ""


class ClientHangUpTest(BridgeCase):
    def test_a_client_that_hangs_up_is_not_a_hermes_error(self) -> None:
        """Interrupting a one-shot ends only the HTTP request; hermes -z runs
        on. Writing its reply to the closed socket must not mark Hermes as
        failed until the next session."""
        import socket
        import struct
        import time

        hung_up = threading.Event()

        def runner(argv, **_kwargs):
            hung_up.wait(5)
            time.sleep(0.2)  # let the reset reach the bridge first
            return subprocess.CompletedProcess(argv, 0, stdout="the answer\n", stderr="")

        self.httpd.command_runner = runner
        body = json.dumps({"text": "a question"}).encode()
        client = socket.create_connection(self.httpd.server_address[:2], timeout=5)
        client.sendall(b"POST /oneshot HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\n"
                       + f"X-Bridge-Token: {FAKE_TOKEN}\r\nContent-Length: {len(body)}\r\n\r\n".encode() + body)
        time.sleep(0.2)
        client.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
        client.close()
        hung_up.set()

        deadline = time.time() + 5
        while time.time() < deadline:
            texts = [m.get("text") for m in self.state.snapshot()["messages"]]
            if "the answer" in texts:
                break
            time.sleep(0.05)
        time.sleep(0.3)  # the response write comes after the reply is stored
        snap = self.state.snapshot()
        self.assertIn("the answer", [m.get("text") for m in snap["messages"]])
        self.assertNotEqual(snap["hermes"]["status"], "error")
        self.assertEqual(snap["bridge"]["error"], "")


class DiffScopeTest(unittest.TestCase):
    def test_diff_against_the_task3_copy_touches_only_the_allowed_functions(self) -> None:
        if shutil.which("git") is None:
            self.skipTest("git not available")
        repo = subprocess.run(["git", "rev-parse", "--show-toplevel"], cwd=HERE,
                              capture_output=True, text=True)
        if repo.returncode != 0:
            self.skipTest("not in a git checkout")
        rel = BRIDGE_PATH.relative_to(Path(repo.stdout.strip())).as_posix()
        old = subprocess.run(["git", "show", f"{TASK3_COMMIT}:{rel}"], cwd=HERE,
                             capture_output=True, text=True)
        if old.returncode != 0:
            self.skipTest("Task 3 commit not available")
        new_lines = BRIDGE_PATH.read_text().splitlines()
        old_lines = old.stdout.splitlines()
        import difflib
        touched = set()
        for tag, i1, i2, j1, j2 in difflib.SequenceMatcher(None, old_lines, new_lines, autojunk=False).get_opcodes():
            if tag == "equal":
                continue
            for j in range(j1, max(j2, j1 + 1)):
                touched.add(_function_at(new_lines, min(j, len(new_lines) - 1)))
            for i in range(i1, max(i2, i1 + 1)):
                touched.add(_function_at(old_lines, min(i, len(old_lines) - 1)))
        self.assertTrue(touched, "the bundled bridge is unchanged from the Task 3 copy")
        self.assertLessEqual(touched, ALLOWED, f"changes outside the approval/model handlers: {sorted(touched - ALLOWED)}")


if __name__ == "__main__":
    unittest.main()
