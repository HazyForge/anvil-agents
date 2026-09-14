#!/usr/bin/env python3
"""Send one native AGY turn, retaining stdin until its result arrives."""
import json
import os
import selectors
import signal
import subprocess
import sys
import time
import threading


def main():
    with open(sys.argv[1], encoding="utf-8") as prompt_file:
        prompt = prompt_file.read()
    deadline = time.monotonic() + int(os.environ.get("ANVIL_AGY_TURN_TIMEOUT_SECONDS", "300"))
    proc = subprocess.Popen(
        ["agy", *sys.argv[2:], "--input-format", "stream-json", "--output-format", "stream-json"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, start_new_session=True, bufsize=0,
    )
    pending = b""
    selector = selectors.DefaultSelector()
    try:
        payload = (json.dumps({"event": "user", "message": {"content": prompt}}) + "\n").encode()
        def send_prompt():
            try:
                offset = 0
                while offset < len(payload):
                    offset += os.write(proc.stdin.fileno(), payload[offset:])
            except (BrokenPipeError, OSError, ValueError):
                pass  # Process/result/timeout closes the pipe; the owner reports its status.
        threading.Thread(target=send_prompt, daemon=True).start()
        selector.register(proc.stdout, selectors.EVENT_READ)
        while selector.get_map():
            if time.monotonic() >= deadline:
                raise TimeoutError("Antigravity turn timed out")
            for key, _ in selector.select(1):
                chunk = os.read(key.fd, 65536)
                if not chunk:
                    selector.unregister(key.fileobj)
                    break
                sys.stdout.buffer.write(chunk)
                sys.stdout.buffer.flush()
                pending += chunk
                while b"\n" in pending:
                    line, pending = pending.split(b"\n", 1)
                    try:
                        event = json.loads(line)
                    except (ValueError, UnicodeDecodeError):
                        continue
                    if isinstance(event, dict) and event.get("event") == "result" and not proc.stdin.closed:
                        proc.stdin.close()
                if len(pending) > 4 * 1024 * 1024:
                    raise ValueError("Antigravity event exceeded 4 MiB")
        return proc.wait(timeout=max(1, deadline - time.monotonic()))
    except (TimeoutError, subprocess.TimeoutExpired):
        print("Antigravity turn timed out", file=sys.stderr)
        return 124
    finally:
        selector.close()
        if not proc.stdin.closed:
            proc.stdin.close()
        if proc.poll() is None:
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                proc.wait()


if __name__ == "__main__":
    sys.exit(main())
