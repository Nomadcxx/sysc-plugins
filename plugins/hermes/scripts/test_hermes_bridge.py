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
import unittest.mock
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
        self.state.bridge_token = FAKE_TOKEN
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
            {"model": "gpt-5", "provider": "   "},
        ):
            with self.subTest(body=body):
                status, reply = self.post("/model", body)
                self.assertEqual(status, 400)
                self.assertEqual(reply.get("error"), "invalid_model")
        self.assertEqual(self.rpc.calls_to("config.set"), [])
        self.assertEqual(self.state.snapshot()["hermes"]["model"], bridge.default_state()["hermes"]["model"])

    def test_an_empty_model_keeps_the_stock_code(self) -> None:
        for body in ({"model": ""}, {"model": "   "}, {}):
            with self.subTest(body=body):
                status, reply = self.post("/model", body)
                self.assertEqual(status, 400)
                self.assertEqual(reply.get("error"), "empty_model")
        self.assertEqual(self.rpc.calls_to("config.set"), [])

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

    def test_a_refused_model_is_not_recorded_as_live(self) -> None:
        """Hermes answers an unknown provider with a JSON-RPC error. The
        stock handler took the error envelope as the result and showed the
        refused model as the live one."""
        before = dict(self.state.snapshot()["hermes"])
        refusal = {"jsonrpc": "2.0", "id": "x", "error": {"code": 5001, "message": "Unknown provider 'nosuch'"}}
        for response in (refusal, {"result": refusal}):
            with self.subTest(response=response):
                self.rpc.responses["config.set"] = response
                status, reply = self.post("/model", {"model": "x1", "provider": "nosuch"})
                self.assertEqual(status, 200)
                self.assertEqual(reply["result"]["error"]["code"], 5001)
                after = self.state.snapshot()["hermes"]
                self.assertEqual((after["model"], after["provider"]), (before["model"], before["provider"]))


class ResumeTest(BridgeCase):
    """The stock resume dropped Hermes's reply, so the next prompt created a
    new session and the resumed conversation was lost."""

    RESUMED = {"session_id": "live-2", "stored_session_id": "stored-1",
               "messages": [{"role": "user", "text": "hi"}, {"role": "assistant", "text": "hello"}],
               "info": {"cwd": "/work", "model": "m1", "provider": "p1"}}

    def write_db(self, rows: list[tuple[str, str, str, int]]) -> None:
        import sqlite3
        conn = sqlite3.connect(str(self.tmp / "home" / "state.db"))
        conn.execute("CREATE TABLE messages (session_id TEXT, role TEXT, content TEXT, timestamp INTEGER)")
        conn.executemany("INSERT INTO messages VALUES (?, ?, ?, ?)", rows)
        conn.commit()
        conn.close()

    def test_resume_binds_the_live_session_and_the_next_prompt_uses_it(self) -> None:
        self.rpc.responses["session.resume"] = {"result": dict(self.RESUMED)}
        status, reply = self.post("/session/resume", {"session_id": "stored-1"})
        self.assertEqual(status, 200)
        session = reply["state"]["session"]
        self.assertEqual((session["id"], session["stored_id"], session["cwd"]), ("live-2", "stored-1", "/work"))
        self.assertEqual([m["text"] for m in reply["state"]["messages"]], ["hi", "hello"])
        status, reply = self.post("/prompt", {"text": "and then?"})
        self.assertEqual(status, 200)
        self.assertEqual(self.rpc.calls_to("session.create"), [])
        self.assertEqual(self.rpc.calls_to("prompt.submit")[-1]["session_id"], "live-2")
        self.assertEqual([m["text"] for m in reply["state"]["messages"]], ["hi", "hello", "and then?"])

    def test_a_reply_with_no_stored_id_keeps_the_requested_one(self) -> None:
        """Hermes reattaching a session that is still live may leave
        stored_session_id blank; Resume latest needs it next time."""
        self.rpc.responses["session.resume"] = {"result": dict(self.RESUMED, stored_session_id="")}
        _, reply = self.post("/session/resume", {"session_id": "stored-1"})
        self.assertEqual(reply["state"]["session"]["id"], "live-2")
        self.assertEqual(reply["state"]["session"]["stored_id"], "stored-1")

    def test_a_reply_with_no_messages_keeps_the_stored_transcript(self) -> None:
        self.write_db([("stored-1", "user", "from the db", 1), ("stored-1", "assistant", "stored reply", 2)])
        self.rpc.responses["session.resume"] = {"result": dict(self.RESUMED, messages=[])}
        _, reply = self.post("/session/resume", {"session_id": "stored-1"})
        self.assertEqual(reply["state"]["session"]["id"], "live-2")
        self.assertEqual([m["text"] for m in reply["state"]["messages"]], ["from the db", "stored reply"])

    def test_failed_resume_leaves_the_current_session_and_history_in_place(self) -> None:
        self.write_db([("stored-1", "user", "unrelated history", 1)])
        self.state.set_session_from_resume({
            "session_id": "active", "stored_session_id": "current",
            "messages": [{"role": "user", "text": "active history"}],
        })
        for result in ({"error": {"code": 4007, "message": "session not found"}},
                       {"result": {"status": "accepted"}}):
            with self.subTest(result=result):
                self.rpc.responses["session.resume"] = result
                status, reply = self.post("/session/resume", {"session_id": "stored-1"})
                self.assertEqual(status, 200)
                self.assertTrue(reply["result"].get("error", {}).get("message"))
                self.assertEqual(reply["state"]["session"]["id"], "active")
                self.assertEqual(reply["state"]["session"]["stored_id"], "current")
                self.assertEqual([m["text"] for m in reply["state"]["messages"]], ["active history"])
        self.post("/prompt", {"text": "follow-up"})
        self.assertEqual(self.rpc.calls_to("prompt.submit")[-1]["session_id"], "active")


class TurnStateTest(BridgeCase):
    def event(self, kind: str, **payload) -> None:
        self.state.apply_event({"method": "event", "params": {
            "type": kind, "session_id": "sid-1", "payload": payload,
        }})

    def test_one_shot_completion_does_not_end_a_normal_turn(self) -> None:
        self.post("/prompt", {"text": "normal"})
        self.event("message.start")
        self.event("message.delta", text="normal reply")
        for code in (0, 1):
            with self.subTest(exit_code=code):
                self.httpd.command_runner = lambda argv, **kw: subprocess.CompletedProcess(
                    argv, code, stdout="one-shot reply", stderr="one-shot error" if code else "")
                status, reply = self.post("/oneshot", {"text": "one-shot"})
                self.assertEqual(status, 200 if code == 0 else 500)
                self.assertTrue(reply["state"]["session"]["running"])
                self.assertTrue(reply["state"]["messages"][1]["streaming"])
        self.event("message.complete")
        self.assertFalse(self.state.snapshot()["session"]["running"])

    def test_busy_follow_ups_are_queued_with_their_own_reply(self) -> None:
        self.post("/prompt", {"text": "first"})
        self.event("message.start")
        self.event("message.delta", text="first reply")
        for text in ("second", "third"):
            self.post("/prompt", {"text": text})
        self.assertTrue(self.rpc.calls_to("prompt.submit")[-1].get("queued"))
        self.event("message.complete")
        for text in ("second", "third"):
            self.event("message.start")
            self.event("message.delta", text=text + " reply")
            self.event("message.complete")
        self.assertEqual([m["text"] for m in self.state.snapshot()["messages"]],
                         ["first", "first reply", "second", "second reply", "third", "third reply"])

    def test_public_health_omits_diagnostics(self) -> None:
        self.state.fail_request(f"diagnostic with {FAKE_TOKEN}")
        self.state.write()
        with urllib.request.urlopen(self.base + "/health", timeout=10) as response:
            self.assertEqual(json.load(response), {"bridge": {"status": "online"}})
        self.assertNotIn(FAKE_TOKEN, self.state.state_file.read_text())
        _, reply = self.request("GET", "/state")
        self.assertEqual(reply["bridge"]["error"], "diagnostic with [redacted]")


class PooledReplyTest(unittest.TestCase):
    """Hermes runs session.list and session.resume on a worker pool:
    dispatch returns None and the worker writes the reply to the transport.
    The stock client answered {"status": "accepted"} and lost the reply."""

    def setUp(self) -> None:
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp, ignore_errors=True)
        (tmp / "home").mkdir()
        (tmp / "home" / "config.yaml").write_text("model:\n  default: m0\n")
        self.state = bridge.HermesState(tmp / "state.json", tmp / "home")
        self.client = bridge.HermesRpcClient(self.state, tmp / "home")
        self.seen: list[dict] = []
        real_apply = self.state.apply_event
        self.state.apply_event = lambda obj: (self.seen.append(obj), real_apply(obj))[1]

    def pooled_server(self, reply: dict | None) -> None:
        def dispatch(req: dict, transport) -> None:
            def run() -> None:
                transport.write({"jsonrpc": "2.0", "method": "event", "params": {"type": "noise"}})
                if reply is not None:
                    transport.write({"jsonrpc": "2.0", "id": req["id"], "result": reply})
            threading.Thread(target=run, daemon=True).start()
            return None
        self.client._server = types.SimpleNamespace(dispatch=dispatch)

    def test_the_pool_reply_is_returned(self) -> None:
        self.pooled_server({"session_id": "live-2"})
        response = self.client.dispatch("session.resume", {"session_id": "stored-1"})
        self.assertEqual(response["result"], {"session_id": "live-2"})
        # The reply is not applied as an event; the event is.
        self.assertEqual([o.get("method") for o in self.seen], ["event"])

    def test_no_reply_falls_back_to_accepted(self) -> None:
        self.pooled_server(None)
        response = self.client.dispatch("session.list", {}, reply_timeout=0.2)
        self.assertEqual(response, {"result": {"status": "accepted"}})

    def test_an_inline_reply_is_returned_without_waiting(self) -> None:
        self.client._server = types.SimpleNamespace(dispatch=lambda req, transport: {"id": req["id"], "result": {"ok": 1}})
        self.assertEqual(self.client.dispatch("session.create", {})["result"], {"ok": 1})


class GatewayLivenessTest(unittest.TestCase):
    """Hermes leaves gateway_state=running after an unplanned kill and checks
    the pid itself; the stock bridge trusted the record, so a dead gateway
    read as running and auto-start never fired."""

    def setUp(self) -> None:
        self.home = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.home, ignore_errors=True)

    def record(self, **fields) -> None:
        (self.home / "gateway_state.json").write_text(json.dumps(dict({"gateway_state": "running"}, **fields)))

    def statuses(self) -> tuple[str, str]:
        return bridge.detect_gateway(self.home)["status"], bridge.scan_gateway_summary(self.home)["status"]

    def dead_pid(self) -> int:
        proc = subprocess.Popen([sys.executable, "-c", "pass"])
        proc.wait()
        return proc.pid

    def test_a_running_record_with_a_live_pid_stays_running(self) -> None:
        import os
        self.record(pid=os.getpid())
        self.assertEqual(self.statuses(), ("running", "running"))

    def test_a_running_record_whose_pid_is_gone_reads_stopped(self) -> None:
        self.record(pid=self.dead_pid())
        self.assertEqual(self.statuses(), ("stopped", "stopped"))

    def test_a_reused_pid_with_another_start_time_reads_stopped(self) -> None:
        import os
        start = bridge.process_start_time(os.getpid())
        if start is None:
            self.skipTest("no /proc start time on this platform")
        self.record(pid=os.getpid(), start_time=start + 1)
        self.assertEqual(self.statuses(), ("stopped", "stopped"))
        self.record(pid=os.getpid(), start_time=start)
        self.assertEqual(self.statuses(), ("running", "running"))

    def test_a_record_with_no_pid_or_a_stopped_state_is_left_alone(self) -> None:
        self.record()
        self.assertEqual(self.statuses(), ("running", "running"))
        self.record(gateway_state="stopped", pid=1)
        self.assertEqual(self.statuses(), ("stopped", "stopped"))


# Functions the local changes may touch in the Task 3 copy (commit a8384bd),
# methods named with their class:
# - Task 3a's approval and model handlers;
# - BridgeRequestHandler._send_json, which keeps a client hang-up from being
#   recorded as a Hermes failure;
# - import_hermes_bootstrap with its callers _load_server and main, which
#   import hermes_bootstrap at startup so newer Hermes finds its dependencies;
# - owner-approved 2026-11-19: _handle_session_resume, which binds the session
#   Hermes resumed; StateTransport and HermesRpcClient.dispatch, which return
#   the reply to an RPC Hermes ran on its worker pool; gateway_record_state,
#   process_start_time and their callers detect_gateway and
#   scan_gateway_summary, which read a gateway whose process is gone as
#   stopped.
# - Owner-approved audit fixes 123–126 cover turn state, failed resume,
#   diagnostic redaction and public health output.
ALLOWED = {
    "HermesState._set_approval", "HermesState.apply_event",
    "BridgeRequestHandler._handle_approval", "BridgeRequestHandler._handle_model",
    "BridgeRequestHandler._send_json", "BridgeRequestHandler._handle_session_resume",
    "HermesRpcClient._load_server", "HermesRpcClient.dispatch",
    "StateTransport.__init__", "StateTransport.write",
    "import_hermes_bootstrap", "main",
    "gateway_record_state", "process_start_time", "detect_gateway", "scan_gateway_summary",
    "health_payload", "run_server", "create_server", "BridgeRequestHandler._check_token",
    "BridgeRequestHandler.do_GET", "BridgeRequestHandler.do_POST", "BridgeRequestHandler._handle_session_create",
    "BridgeRequestHandler._handle_prompt", "BridgeRequestHandler._handle_interrupt", "BridgeRequestHandler._handle_oneshot",
    "HermesState.__init__", "HermesState.snapshot", "HermesState.write", "HermesState.redact_diagnostic",
    "HermesState._start_message", "HermesState._complete_message", "HermesState._insert_reply",
    "HermesState.set_session_from_create", "HermesState.append_user_message", "HermesState.append_assistant_message",
    "HermesState.finish_oneshot", "HermesState.stop_pending_turns", "HermesState.discard_prompt",
}
TASK3_COMMIT = "a8384bdeeff6d2f65b8ccbe475ed89fed2cd2329"


def _function_at(lines: list[str], index: int) -> str:
    """The top-level function or Class.method enclosing line index (0-based),
    or '' outside any. Nested helpers count as part of their method."""
    for i in range(min(index, len(lines) - 1), -1, -1):
        m = re.match(r"( {0,4})def (\w+)\(", lines[i])
        if m:
            if not m.group(1):
                return m.group(2)
            for j in range(i - 1, -1, -1):
                c = re.match(r"class (\w+)", lines[j])
                if c:
                    return f"{c.group(1)}.{m.group(2)}"
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


class LoadServerTest(unittest.TestCase):
    """Newer Hermes (b200f5b) keeps its dependencies where only
    hermes_bootstrap puts them on sys.path; every Hermes entry point imports
    it first. Older Hermes has no such module."""

    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.home = self.tmp / "home"
        self.agent = self.home / "hermes-agent"
        (self.agent / "tui_gateway").mkdir(parents=True)
        (self.agent / "tui_gateway" / "__init__.py").write_text("")
        (self.agent / "tui_gateway" / "server.py").write_text(
            "import fake_hermes_dep\nMARK = fake_hermes_dep.VALUE\n")
        self.deps = self.tmp / "managed-deps"
        self.deps.mkdir()
        (self.deps / "fake_hermes_dep.py").write_text("VALUE = 'loaded'\n")
        saved_path = list(sys.path)
        saved_env = dict(bridge.os.environ)

        def restore() -> None:
            sys.path[:] = saved_path
            bridge.os.environ.clear()
            bridge.os.environ.update(saved_env)
            for name in ("tui_gateway", "tui_gateway.server", "hermes_bootstrap", "fake_hermes_dep"):
                sys.modules.pop(name, None)

        restore()
        self.addCleanup(restore)

    def load(self):
        state = bridge.HermesState(self.tmp / "s.json", self.home)
        return bridge.HermesRpcClient(state, self.home)._load_server()

    def test_hermes_bootstrap_is_imported_before_tui_gateway(self) -> None:
        (self.agent / "hermes_bootstrap.py").write_text(
            f"import sys\nsys.path.insert(0, {str(self.deps)!r})\n")
        self.assertEqual(self.load().MARK, "loaded")

    def test_an_older_hermes_without_hermes_bootstrap_still_loads(self) -> None:
        sys.path.insert(0, str(self.deps))
        self.assertEqual(self.load().MARK, "loaded")

    def test_a_failing_bootstrap_is_reported_as_hermes_unavailable(self) -> None:
        """hermes_bootstrap raises SystemExit when Hermes needs `pm repair`;
        in a request thread that would end the thread with no reply."""
        for body, want in (("raise SystemExit(1)\n", "hermes_unavailable"),
                           ("import missing_pm_package\n", "missing_pm_package")):
            with self.subTest(body=body):
                sys.modules.pop("hermes_bootstrap", None)
                (self.agent / "hermes_bootstrap.py").write_text(body)
                with self.assertRaises(RuntimeError) as caught:
                    self.load()
                self.assertIn("hermes_unavailable", str(caught.exception))
                self.assertIn(want, str(caught.exception))

    def run_main(self) -> list[str]:
        """main() with the re-exec and the server replaced by recorders."""
        order: list[str] = []
        saved = (bridge.reexec_with_hermes_python, bridge.run_server)
        self.addCleanup(lambda: (setattr(bridge, "reexec_with_hermes_python", saved[0]),
                                 setattr(bridge, "run_server", saved[1])))
        bridge.reexec_with_hermes_python = lambda *_a: order.append("reexec")
        bridge.run_server = lambda *_a: order.append(
            "serve:" + ("bootstrapped" if "hermes_bootstrap" in sys.modules else "bare"))
        self.assertEqual(bridge.main(["--hermes-home", str(self.home), "--port", "1"]), 0)
        return order

    def test_startup_runs_the_bootstrap_before_serving(self) -> None:
        """hermes_bootstrap may re-exec the process into Hermes's managed
        Python. At startup that costs nothing; on the first RPC it drops the
        request and the bridge's state."""
        (self.agent / "hermes_bootstrap.py").write_text("")
        self.assertEqual(self.run_main(), ["reexec", "serve:bootstrapped"])

    def test_a_failing_bootstrap_at_startup_still_serves(self) -> None:
        (self.agent / "hermes_bootstrap.py").write_text("raise SystemExit(1)\n")
        with unittest.mock.patch("sys.stderr"):
            self.assertEqual(self.run_main(), ["reexec", "serve:bare"])
        with self.assertRaises(RuntimeError):
            self.load()


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
            # A change made only of comment lines (the provenance header)
            # alters no behavior.
            if all(line.lstrip().startswith("#") for line in old_lines[i1:i2] + new_lines[j1:j2]):
                continue
            for j in range(j1, max(j2, j1 + 1)):
                touched.add(_function_at(new_lines, min(j, len(new_lines) - 1)))
            for i in range(i1, max(i2, i1 + 1)):
                touched.add(_function_at(old_lines, min(i, len(old_lines) - 1)))
        self.assertTrue(touched, "the bundled bridge is unchanged from the Task 3 copy")
        self.assertLessEqual(touched, ALLOWED, f"changes outside the approval/model handlers: {sorted(touched - ALLOWED)}")


if __name__ == "__main__":
    unittest.main()
