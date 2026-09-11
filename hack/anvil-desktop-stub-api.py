#!/usr/bin/env python3
"""Loopback anvil-agents API stub for Anvil Agents Desktop VM tests.

Serves /healthz and /ui-config.json only. It is not a Kubernetes apiserver
and does not accept tokens in query strings. Use this when the live OIDC API
origin is unreachable so the login surface still loads.
"""

from __future__ import annotations

import argparse
import json
from http.server import BaseHTTPRequestHandler, HTTPServer


def main() -> None:
    parser = argparse.ArgumentParser(description="Stub anvil-agents OIDC API for desktop VM tests.")
    parser.add_argument("--listen", default="127.0.0.1:1739")
    args = parser.parse_args()
    host, port_s = args.listen.rsplit(":", 1)
    port = int(port_s)

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, fmt: str, *arguments: object) -> None:
            return

        def do_GET(self) -> None:  # noqa: N802
            path = self.path.split("?", 1)[0]
            query = self.path.split("?", 1)[1] if "?" in self.path else ""
            if "access_token=" in query or "id_token=" in query or "refresh_token=" in query:
                self._json(400, {"error": "token_in_query", "message": "access tokens must not be placed in query strings"})
                return
            if path == "/healthz":
                self._json(200, {"status": "ok"})
                return
            if path == "/ui-config.json":
                self._json(
                    200,
                    {
                        "productTitle": "Anvil Agents Console",
                        "defaultNamespaces": ["hazy-trade"],
                        "oidc": {
                            "issuer": "https://auth.example.invalid",
                            "clientId": "anvil-agents-desktop-stub",
                            "audiences": ["anvil-agents-api"],
                            "scopes": ["openid", "profile", "email", "offline_access"],
                        },
                        "composition": {"readEnabled": False, "writeEnabled": False},
                        "runs": {"createEnabled": False},
                    },
                )
                return
            self._json(404, {"error": "not_found", "message": "stub API has no cluster resources"})

        def _json(self, status: int, body: dict) -> None:
            raw = json.dumps(body).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

    HTTPServer((host, port), Handler).serve_forever()


if __name__ == "__main__":
    main()
