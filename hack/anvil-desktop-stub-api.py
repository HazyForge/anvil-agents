#!/usr/bin/env python3
"""Loopback anvil-agents API stub for Anvil Agents Desktop VM tests.

Implements enough of the OIDC API for create-agent + standing chat:
ui-config, composition AgentRunProfiles, and PR 168 chat threads/messages.
It is not a Kubernetes apiserver. Tokens must not appear in query strings.

When the live Kind API is unreachable, Desktop can open a stub session
against this origin. Production Zitadel is not this stub.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import threading
import uuid
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any
from urllib.parse import parse_qs, urlparse
from urllib.request import Request, urlopen


MANAGED_BY = "control.anvil.hazyforge.io/managed-by"
MANAGED_VALUE = "anvil-agents-console"
API_VERSION = "control.anvil.hazyforge.io/v1alpha1"

PROFILE_LIST = re.compile(r"^/api/v1/namespaces/([^/]+)/agent-run-profiles$")
PROFILE_ITEM = re.compile(r"^/api/v1/namespaces/([^/]+)/agent-run-profiles/([^/]+)$")
THREAD_LIST = re.compile(r"^/api/v1/namespaces/([^/]+)/chat/threads$")
THREAD_ITEM = re.compile(r"^/api/v1/namespaces/([^/]+)/chat/threads/([^/]+)$")
THREAD_MSGS = re.compile(r"^/api/v1/namespaces/([^/]+)/chat/threads/([^/]+)/messages$")
RUN_LIST = re.compile(r"^/api/v1/namespaces/([^/]+)/agent-runs$")
RUN_ITEM = re.compile(r"^/api/v1/namespaces/([^/]+)/agent-runs/([^/]+)$")


def utcnow() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def dns_label(raw: str) -> bool:
    name = (raw or "").strip()
    return bool(re.fullmatch(r"[a-z0-9]([-a-z0-9]*[a-z0-9])?", name)) and len(name) <= 63


class StubStore:
    def __init__(self) -> None:
        self.profiles: dict[tuple[str, str], dict[str, Any]] = {}
        self.threads: dict[str, dict[str, Any]] = {}
        self.messages: dict[str, list[dict[str, Any]]] = {}
        self.runs: dict[tuple[str, str], dict[str, Any]] = {}

    def profile_doc(self, namespace: str, name: str, spec: dict[str, Any]) -> dict[str, Any]:
        return {
            "apiVersion": API_VERSION,
            "kind": "AgentRunProfile",
            "metadata": {
                "name": name,
                "namespace": namespace,
                "labels": {MANAGED_BY: MANAGED_VALUE},
            },
            "spec": spec,
            "management": {
                "writable": True,
                "reason": "console_managed",
                "managedBy": MANAGED_VALUE,
            },
        }


def stub_assistant_content(user_content: str) -> str:
    trimmed = user_content.strip()
    if not trimmed:
        return "(stub) standing chat storage is enabled; LangGraph execution is not wired yet."
    return (
        "(stub) standing chat storage is enabled; LangGraph execution is not wired yet.\n\nYou said:\n"
        + trimmed
    )


def make_handler(store: StubStore) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, fmt: str, *arguments: object) -> None:
            return

        def _json(self, status: int, body: dict[str, Any] | list[Any]) -> None:
            raw = json.dumps(body).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def _error(self, status: int, code: str, message: str) -> None:
            self._json(status, {"error": {"code": code, "message": message}})

        def _query_has_token(self, query: str) -> bool:
            return "access_token=" in query or "id_token=" in query or "refresh_token=" in query

        def _require_bearer(self) -> bool:
            auth = self.headers.get("Authorization") or ""
            if not auth.lower().startswith("bearer ") or not auth[7:].strip():
                self._error(401, "unauthorized", "missing bearer token")
                return False
            return True

        def _read_json(self) -> dict[str, Any] | None:
            try:
                length = int(self.headers.get("Content-Length") or "0")
            except ValueError:
                self._error(400, "invalid_body", "invalid content-length")
                return None
            if length > 256 * 1024:
                self._error(400, "too_large", "request body exceeds 256KiB")
                return None
            raw = self.rfile.read(length) if length else b"{}"
            try:
                data = json.loads(raw.decode("utf-8") or "{}")
            except json.JSONDecodeError:
                self._error(400, "invalid_json", "decode JSON body")
                return None
            if not isinstance(data, dict):
                self._error(400, "invalid_json", "JSON object required")
                return None
            return data

        def do_GET(self) -> None:  # noqa: N802
            parsed = urlparse(self.path)
            path = parsed.path
            query = parsed.query
            if self._query_has_token(query):
                self._error(400, "token_in_query", "access tokens must not be placed in query strings")
                return
            if path == "/healthz":
                self._json(200, {"status": "ok"})
                return
            if path == "/ui-config.json":
                self._json(
                    200,
                    {
                        "productTitle": "Anvil Agents Console",
                        "defaultNamespaces": ["agents"],
                        "oidc": {
                            "issuer": "https://auth.example.invalid",
                            "clientId": "anvil-agents-desktop-stub",
                            "audiences": ["anvil-agents-api"],
                            "scopes": ["openid", "profile", "email", "offline_access"],
                        },
                        "composition": {"readEnabled": True, "writeEnabled": True},
                        "runs": {"createEnabled": False},
                        "chat": {"enabled": True},
                        "desktop": {"stubSession": True},
                    },
                )
                return
            if not self._require_bearer():
                return
            params = parse_qs(query)

            m = PROFILE_LIST.match(path)
            if m:
                ns = m.group(1)
                items = [doc for (namespace, _), doc in store.profiles.items() if namespace == ns]
                items.sort(key=lambda d: d["metadata"]["name"])
                limit = 50
                if params.get("limit"):
                    try:
                        limit = int(params["limit"][0])
                    except ValueError:
                        self._error(400, "invalid_limit", "limit must be between 1 and 200")
                        return
                self._json(200, {"items": items[:limit]})
                return
            m = PROFILE_ITEM.match(path)
            if m:
                key = (m.group(1), m.group(2))
                doc = store.profiles.get(key)
                if not doc:
                    self._error(404, "not_found", "resource not found")
                    return
                self._json(200, doc)
                return
            m = THREAD_LIST.match(path)
            if m:
                ns = m.group(1)
                mode = (params.get("mode") or [""])[0]
                profile = (params.get("profileName") or [""])[0]
                items = [
                    t
                    for t in store.threads.values()
                    if t["namespace"] == ns
                    and (not mode or t.get("mode") == mode)
                    and (not profile or t.get("profileName") == profile)
                ]
                items.sort(key=lambda t: t.get("updatedAt") or "", reverse=True)
                self._json(200, {"items": items[:200]})
                return
            m = THREAD_ITEM.match(path)
            if m:
                ns, thread_id = m.group(1), m.group(2)
                thread = store.threads.get(thread_id)
                if not thread or thread["namespace"] != ns:
                    self._error(404, "not_found", "resource not found")
                    return
                msgs = store.messages.get(thread_id, [])
                self._json(200, {**thread, "messages": msgs})
                return
            m = THREAD_MSGS.match(path)
            if m:
                ns, thread_id = m.group(1), m.group(2)
                thread = store.threads.get(thread_id)
                if not thread or thread["namespace"] != ns:
                    self._error(404, "not_found", "resource not found")
                    return
                self._json(200, {"items": store.messages.get(thread_id, [])})
                return
            m = RUN_LIST.match(path)
            if m:
                ns = m.group(1)
                items = [run for (namespace, _), run in store.runs.items() if namespace == ns]
                self._json(200, {"items": items})
                return
            m = RUN_ITEM.match(path)
            if m:
                self._error(404, "not_found", "resource not found")
                return
            self._error(404, "not_found", "stub API has no cluster resources")

        def do_POST(self) -> None:  # noqa: N802
            parsed = urlparse(self.path)
            path = parsed.path
            if self._query_has_token(parsed.query):
                self._error(400, "token_in_query", "access tokens must not be placed in query strings")
                return
            if not self._require_bearer():
                return
            body = self._read_json()
            if body is None:
                return

            m = PROFILE_LIST.match(path)
            if m:
                ns = m.group(1)
                meta = body.get("metadata") if isinstance(body.get("metadata"), dict) else {}
                name = str(meta.get("name") or "").strip()
                if not dns_label(name):
                    self._error(400, "invalid_body", "metadata.name is required")
                    return
                if meta.get("namespace") and str(meta.get("namespace")) != ns:
                    self._error(400, "invalid_body", "metadata.namespace must match the URL namespace")
                    return
                key = (ns, name)
                if key in store.profiles:
                    self._error(409, "conflict", "resource already exists")
                    return
                spec = body.get("spec") if isinstance(body.get("spec"), dict) else {}
                doc = store.profile_doc(ns, name, spec)
                store.profiles[key] = doc
                self._json(201, doc)
                return

            m = THREAD_LIST.match(path)
            if m:
                ns = m.group(1)
                mode = str(body.get("mode") or "persona").strip() or "persona"
                profile = str(body.get("profileName") or "").strip()
                if mode == "persona" and not profile:
                    self._error(400, "invalid", "profileName is required for persona threads")
                    return
                thread_id = str(uuid.uuid4())
                now = utcnow()
                thread = {
                    "id": thread_id,
                    "namespace": ns,
                    "profileName": profile,
                    "mode": mode,
                    "title": str(body.get("title") or "").strip(),
                    "createdAt": now,
                    "updatedAt": now,
                    "createdBy": "stub-desktop",
                    "metadata": body.get("metadata"),
                }
                store.threads[thread_id] = thread
                store.messages[thread_id] = []
                self._json(201, thread)
                return

            m = THREAD_MSGS.match(path)
            if m:
                ns, thread_id = m.group(1), m.group(2)
                thread = store.threads.get(thread_id)
                if not thread or thread["namespace"] != ns:
                    self._error(404, "not_found", "resource not found")
                    return
                content = str(body.get("content") or "")
                if not content.strip():
                    self._error(400, "invalid", "content is required")
                    return
                if len(content.encode("utf-8")) > 64 * 1024:
                    self._error(400, "invalid", "content exceeds 64KiB")
                    return
                msgs = store.messages.setdefault(thread_id, [])
                seq = (msgs[-1]["sequence"] if msgs else 0) + 1
                now = utcnow()
                user = {
                    "id": str(uuid.uuid4()),
                    "threadId": thread_id,
                    "role": "user",
                    "content": content,
                    "createdAt": now,
                    "sequence": seq,
                    "metadata": body.get("metadata"),
                }
                assistant = {
                    "id": str(uuid.uuid4()),
                    "threadId": thread_id,
                    "role": "assistant",
                    "content": stub_assistant_content(content),
                    "createdAt": now,
                    "sequence": seq + 1,
                    "metadata": {"stub": True, "engine": "echo"},
                }
                msgs.extend([user, assistant])
                thread["updatedAt"] = now
                self._json(201, {"thread": thread, "user": user, "assistant": assistant})
                return

            self._error(404, "not_found", "stub API has no cluster resources")

    return Handler


def serve(listen: str, store: StubStore | None = None) -> HTTPServer:
    host, port_s = listen.rsplit(":", 1)
    server = HTTPServer((host, int(port_s)), make_handler(store or StubStore()))
    return server


def self_test() -> int:
    store = StubStore()
    server = HTTPServer(("127.0.0.1", 0), make_handler(store))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    host, port = server.server_address[:2]
    base = f"http://{host}:{port}"
    failures = 0

    def call(method: str, path: str, token: str | None = None, payload: dict[str, Any] | None = None) -> tuple[int, Any]:
        data = None if payload is None else json.dumps(payload).encode("utf-8")
        headers = {"Accept": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = Request(base + path, data=data, headers=headers, method=method)
        try:
            with urlopen(req) as resp:
                return resp.status, json.loads(resp.read().decode("utf-8"))
        except Exception as exc:  # urllib HTTPError
            raw = getattr(exc, "read", lambda: b"")()
            status = int(getattr(exc, "code", 0) or 0)
            try:
                return status, json.loads(raw.decode("utf-8"))
            except Exception:
                return status, {"error": str(exc)}

    status, _ = call("GET", "/healthz")
    if status != 200:
        print("healthz failed", status, file=sys.stderr)
        failures += 1
    status, cfg = call("GET", "/ui-config.json")
    if status != 200 or not cfg.get("chat", {}).get("enabled") or not cfg.get("composition", {}).get("writeEnabled"):
        print("ui-config missing chat/write", cfg, file=sys.stderr)
        failures += 1
    status, _ = call("GET", "/api/v1/namespaces/agents/agent-run-profiles")
    if status != 401:
        print("expected 401 without bearer", status, file=sys.stderr)
        failures += 1
    status, created = call(
        "POST",
        "/api/v1/namespaces/agents/agent-run-profiles",
        "stub",
        {"metadata": {"name": "scout"}, "spec": {"description": "spawned"}},
    )
    if status != 201 or created.get("metadata", {}).get("name") != "scout":
        print("create profile failed", status, created, file=sys.stderr)
        failures += 1
    status, _ = call(
        "POST",
        "/api/v1/namespaces/agents/agent-run-profiles",
        "stub",
        {"metadata": {"name": "scout"}, "spec": {}},
    )
    if status != 409:
        print("expected conflict", status, file=sys.stderr)
        failures += 1
    status, thread = call(
        "POST",
        "/api/v1/namespaces/agents/chat/threads",
        "stub",
        {"mode": "persona", "profileName": "scout", "title": "Scout"},
    )
    if status != 201 or not thread.get("id"):
        print("create thread failed", status, thread, file=sys.stderr)
        failures += 1
        server.shutdown()
        return 1
    status, appended = call(
        "POST",
        f"/api/v1/namespaces/agents/chat/threads/{thread['id']}/messages",
        "stub",
        {"content": "hello", "metadata": {"entity": True, "authorProfile": "scout"}},
    )
    if status != 201 or not appended.get("assistant", {}).get("metadata", {}).get("stub"):
        print("append failed", status, appended, file=sys.stderr)
        failures += 1
    status, listed = call("GET", "/api/v1/namespaces/agents/agent-run-profiles?limit=20", "stub")
    if status != 200 or len(listed.get("items") or []) != 1:
        print("list profiles failed", status, listed, file=sys.stderr)
        failures += 1
    status, _ = call("GET", "/api/v1/namespaces/agents/agent-run-profiles?access_token=secret", "stub")
    if status != 400:
        print("token query not rejected", status, file=sys.stderr)
        failures += 1
    server.shutdown()
    if failures:
        print(f"stub self-test failures: {failures}", file=sys.stderr)
        return 1
    print("stub self-test ok")
    return 0


def main() -> None:
    parser = argparse.ArgumentParser(description="Stub anvil-agents OIDC API for desktop VM tests.")
    parser.add_argument("--listen", default="127.0.0.1:1739")
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        raise SystemExit(self_test())
    server = serve(args.listen)
    server.serve_forever()


if __name__ == "__main__":
    main()
