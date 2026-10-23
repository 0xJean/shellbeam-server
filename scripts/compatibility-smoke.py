#!/usr/bin/env python3
"""Exercise an isolated ShellBeam Server using only Python's standard library.

Usage: python3 scripts/compatibility-smoke.py --binary /absolute/path/to/shellbeam-server

Creates private, temporary SQLite databases and a loopback listener. It never
opens existing server data, installs a service, starts the App, or configures a
notification destination. Credentials, HTTP bodies, and server logs are never
printed. All tests use real server routes; databases are not edited directly.
"""

import argparse
import base64
import hashlib
import http.client
from http.cookies import SimpleCookie
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import time
from urllib.parse import quote


MAX_RESPONSE = 2 * 1024 * 1024
REQUEST_TIMEOUT = 5


def require(condition):
    if not condition:
        raise AssertionError("contract check failed")


def response_data(body):
    return body.get("data", body) if isinstance(body, dict) else body


class WebSocket:
    """Small bounded RFC 6455 client for local, uncompressed JSON-RPC only."""

    def __init__(self, port, path):
        self.socket = socket.create_connection(("127.0.0.1", port), REQUEST_TIMEOUT)
        self.socket.settimeout(REQUEST_TIMEOUT)
        self.buffer = bytearray()
        try:
            key = base64.b64encode(secrets.token_bytes(16)).decode("ascii")
            self.socket.sendall((
                "GET " + path + " HTTP/1.1\r\n"
                "Host: 127.0.0.1:" + str(port) + "\r\n"
                "Upgrade: websocket\r\n"
                "Connection: Upgrade\r\n"
                "Sec-WebSocket-Key: " + key + "\r\n"
                "Sec-WebSocket-Version: 13\r\n\r\n"
            ).encode("ascii"))
            deadline = time.monotonic() + REQUEST_TIMEOUT
            while b"\r\n\r\n" not in self.buffer:
                self.socket.settimeout(max(0.01, deadline - time.monotonic()))
                chunk = self.socket.recv(4096)
                require(bool(chunk) and time.monotonic() < deadline)
                self.buffer.extend(chunk)
                require(len(self.buffer) <= 16384)
            header, remaining = bytes(self.buffer).split(b"\r\n\r\n", 1)
            self.buffer = bytearray(remaining)
            lines = header.decode("ascii").split("\r\n")
            require(lines[0].split(" ")[1] == "101")
            headers = dict(line.split(":", 1) for line in lines[1:] if ":" in line)
            headers = {key.lower(): value.strip() for key, value in headers.items()}
            expected = base64.b64encode(hashlib.sha1(
                (key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode("ascii")
            ).digest()).decode("ascii")
            require(headers.get("sec-websocket-accept") == expected)
            require(headers.get("upgrade", "").lower() == "websocket")
            require("sec-websocket-extensions" not in headers)
        except BaseException:
            self.socket.close()
            raise

    def exact(self, count):
        while len(self.buffer) < count:
            chunk = self.socket.recv(min(count - len(self.buffer), 65536))
            if not chunk:
                raise EOFError("websocket closed")
            self.buffer.extend(chunk)
        result = bytes(self.buffer[:count])
        del self.buffer[:count]
        return result

    def send(self, payload, opcode=1):
        mask = secrets.token_bytes(4)
        size = len(payload)
        require(size <= MAX_RESPONSE)
        if size < 126:
            prefix = bytes([0x80 | opcode, 0x80 | size])
        elif size < 65536:
            prefix = bytes([0x80 | opcode, 0x80 | 126]) + struct.pack("!H", size)
        else:
            prefix = bytes([0x80 | opcode, 0x80 | 127]) + struct.pack("!Q", size)
        self.socket.sendall(prefix + mask + bytes(
            value ^ mask[index % 4] for index, value in enumerate(payload)
        ))

    def receive_json(self, deadline):
        message = bytearray()
        started = False
        while time.monotonic() < deadline:
            self.socket.settimeout(max(0.01, deadline - time.monotonic()))
            first, second = self.exact(2)
            opcode, final = first & 15, bool(first & 0x80)
            require(not (first & 0x70) and not (second & 0x80))
            size = second & 0x7f
            if size == 126:
                size = struct.unpack("!H", self.exact(2))[0]
            elif size == 127:
                size = struct.unpack("!Q", self.exact(8))[0]
            require(size <= MAX_RESPONSE)
            if opcode >= 8:
                require(final and size <= 125)
            payload = self.exact(size)
            if opcode == 8:
                raise EOFError("websocket closed")
            if opcode == 9:
                self.send(payload, 10)
                continue
            if opcode == 10:
                continue
            require(opcode == 1 if not started else opcode == 0)
            started = True
            message.extend(payload)
            require(len(message) <= MAX_RESPONSE)
            if final:
                return json.loads(message.decode("utf-8"))
        raise TimeoutError("websocket deadline expired")

    def rpc(self, method, params, request_id):
        self.send(json.dumps({"jsonrpc": "2.0", "id": request_id,
                              "method": method, "params": params}).encode("utf-8"))
        deadline = time.monotonic() + REQUEST_TIMEOUT
        for _ in range(16):
            body = self.receive_json(deadline)
            if isinstance(body, dict) and body.get("id") == request_id:
                require(body.get("jsonrpc") == "2.0" and "error" not in body)
                return body.get("result")
        raise TimeoutError("websocket response deadline expired")

    def close(self):
        try:
            self.socket.settimeout(1)
            self.send(struct.pack("!H", 1000), 8)
        except OSError:
            pass
        finally:
            self.socket.close()


class Smoke:
    def __init__(self, binary, root, log):
        self.binary, self.root, self.log = binary, root, log
        self.process = None
        self.stage = "allocate-loopback-port"
        self.checks = []
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            self.port = listener.getsockname()[1]
        self.origin = "http://127.0.0.1:" + str(self.port)

    def passed(self, name):
        self.checks.append(name)

    def request(self, path, body=None, headers=None, method=None):
        connection = http.client.HTTPConnection("127.0.0.1", self.port,
                                                 timeout=REQUEST_TIMEOUT)
        request_headers = {"Accept": "application/json"}
        request_headers.update(headers or {})
        encoded = None if body is None else json.dumps(body).encode("utf-8")
        if encoded is not None:
            request_headers["Content-Type"] = "application/json"
        try:
            connection.request(method or ("GET" if body is None else "POST"),
                               path, body=encoded, headers=request_headers)
            response = connection.getresponse()
            raw = response.read(MAX_RESPONSE + 1)
            require(len(raw) <= MAX_RESPONSE)
            try:
                parsed = json.loads(raw) if raw else None
            except (ValueError, UnicodeDecodeError):
                parsed = None
            return response.status, dict(response.getheaders()), parsed
        finally:
            connection.close()

    def successful(self, path, body=None, headers=None):
        status, _, parsed = self.request(path, body, headers)
        require(status == 200 and isinstance(parsed, (dict, list)))
        if isinstance(parsed, dict):
            require("error" not in parsed)
            require(parsed.get("status", "success") == "success")
        return response_data(parsed)

    def rpc(self, method, params, headers):
        status, _, body = self.request("/api/rpc2", {
            "jsonrpc": "2.0", "id": 1, "method": method, "params": params,
        }, headers)
        require(status == 200 and isinstance(body, dict))
        require(body.get("jsonrpc") == "2.0" and body.get("id") == 1)
        require("error" not in body and "result" in body)
        return body["result"]

    def wait_for(self, check, timeout=30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.process is not None and self.process.poll() is not None:
                raise ChildProcessError("isolated server exited")
            try:
                result = check()
                if result:
                    return result
            except (OSError, ValueError, http.client.HTTPException, AssertionError):
                pass
            time.sleep(0.1)
        raise TimeoutError("bounded wait expired")

    def start(self):
        self.process = subprocess.Popen([
            str(self.binary), "server", "--listen", "127.0.0.1:" + str(self.port),
            "--database", str(self.root / "primary.db"),
        ], cwd=str(self.root), env={"PATH": os.defpath, "TZ": "UTC"},
            stdin=subprocess.DEVNULL, stdout=self.log, stderr=self.log)

    def stop(self):
        if self.process is None:
            return True
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        self.process.wait(timeout=1)
        return self.process.poll() is not None

    def version(self):
        version = self.successful("/api/version")
        require(isinstance(version, dict) and version.get("version") == "1.6.0")
        require(version.get("product") == "ShellBeam Server")
        for name in ("product_version", "upstream_version", "upstream_commit"):
            require(isinstance(version.get(name), str) and bool(version[name]))
        require(isinstance(version.get("hash"), str))
        return version

    def nodes(self, auth):
        nodes = self.successful("/api/admin/client/list", headers=auth)
        require(isinstance(nodes, list))
        return nodes

    def settings(self, auth):
        settings = self.successful("/api/admin/settings/", headers=auth)
        require(isinstance(settings, dict))
        return settings

    @staticmethod
    def report(cpu):
        return {
            "cpu": {"usage": cpu}, "ram": {"total": 8192, "used": 4096},
            "swap": {"total": 0, "used": 0},
            "disk": {"total": 100000, "used": 20000},
            "load": {"load1": 0.5, "load5": 0.3, "load15": 0.2},
            "network": {"up": 2048, "down": 1024, "totalUp": 20480,
                        "totalDown": 10240},
            "connections": {"tcp": 2, "udp": 1}, "uptime": 3600, "process": 5,
        }

    def agent_http(self, token, cpu):
        status, _, body = self.request("/api/clients/v2/rpc", {
            "token": token, "jsonrpc": "2.0", "id": cpu,
            "method": "agent.report", "params": {"report": self.report(cpu)},
        })
        require(status == 200 and isinstance(body, dict))
        require(body.get("jsonrpc") == "2.0" and body.get("id") == cpu)
        require("error" not in body and body.get("result", {}).get("status") == "success")

    def raw_samples(self, node_id, auth, expected):
        data = self.rpc("public:queryMetrics", {
            "metric_key": "cpu.usage", "entity_id": node_id,
            "hours": 0.1, "fill_empty": False, "max_points": 1000,
        }, auth)
        require(isinstance(data, dict) and isinstance(data.get("series"), list))
        points = []
        for series in data["series"]:
            if series.get("metric_key") == "cpu.usage" and series.get("entity_id") == node_id:
                require(series.get("downsampled") is False)
                samples = series.get("points")
                # Empty Go slices are null before the asynchronous batch flush.
                require(samples is None or isinstance(samples, list))
                points.extend(samples or [])
        require(all(isinstance(point.get("time"), str) for point in points))
        values = [point.get("value") for point in points]
        return points if all(value in values for value in expected) else None

    def legacy_history(self, node_id, auth):
        # A one-hour query selects the minute tier. Wider windows can select a
        # coarse tier whose first bucket has not closed in a new installation.
        data = self.successful("/api/records/load?uuid=" + quote(node_id, safe="")
                               + "&load_type=all&hours=1", headers=auth)
        require(isinstance(data, dict) and isinstance(data.get("records"), list))
        records = data["records"]
        require(all(isinstance(record.get("cpu"), (int, float))
                    and 10 <= record["cpu"] <= 40 for record in records))
        return records or None

    def run(self):
        self.stage = "first-run-install-status"
        self.start()
        self.wait_for(lambda: self.successful("/api/install/status").get("required") is True)
        self.passed("first-run-install-status")

        username = "smoke-" + secrets.token_hex(8)
        password = "Sb1" + secrets.token_hex(24)
        api_key = secrets.token_hex(32)
        site_name = "Custom private smoke site " + secrets.token_hex(6)
        self.stage = "first-run-install-complete"
        self.successful("/api/install/complete", {
            "username": username, "password": password, "sitename": site_name,
            "description": "Synthetic local-only compatibility check",
            "metric_dsn": str(self.root / "metrics.db"),
        })
        initial_version = self.wait_for(self.version)
        self.passed("first-run-install-complete")
        self.passed("legacy-version-and-product-metadata")

        self.stage = "session-login-and-api-key-settings"
        status, headers, _ = self.request("/api/login", {"username": username, "password": password})
        require(status == 200)
        cookies = SimpleCookie()
        cookies.load(next((value for key, value in headers.items()
                           if key.lower() == "set-cookie"), ""))
        require("session_token" in cookies and bool(cookies["session_token"].value))
        session = {"Cookie": "session_token=" + cookies["session_token"].value,
                   "Origin": self.origin}
        require(self.settings(session).get("sitename") == site_name)
        self.successful("/api/admin/settings/", {
            "api_key": api_key, "private_site": True, "geo_ip_enabled": False,
            "notification_enabled": False, "login_notification": False,
            "eula_accepted": True,
        }, session)
        auth = {"Authorization": "Bearer " + api_key}
        require(self.settings(auth).get("sitename") == site_name)
        require(self.settings(auth).get("notification_enabled") is False)
        require(self.nodes(auth) == [])
        self.passed("session-cookie-login")
        self.passed("session-api-key-save-and-custom-sitename-preservation")
        self.passed("bearer-client-list")

        self.stage = "tauri-cors-and-preflight"
        status, headers, _ = self.request("/api/admin/client/list", headers={
            **auth, "Origin": "tauri://localhost",
        })
        headers = {key.lower(): value for key, value in headers.items()}
        require(status == 200 and headers.get("access-control-allow-origin") == "tauri://localhost")
        status, headers, _ = self.request("/api/admin/client/list", method="OPTIONS", headers={
            "Origin": "tauri://localhost", "Access-Control-Request-Method": "GET",
            "Access-Control-Request-Headers": "authorization",
        })
        headers = {key.lower(): value for key, value in headers.items()}
        require(status == 204 and headers.get("access-control-allow-origin") == "tauri://localhost")
        require("authorization" in headers.get("access-control-allow-headers", "").lower())
        self.passed("tauri-origin-cors-and-authorization-preflight")

        self.stage = "synthetic-client-creation"
        created = self.successful("/api/admin/client/add", {"name": "synthetic-smoke-node"}, auth)
        require(isinstance(created, dict))
        node_id, agent_token = created.get("uuid"), created.get("token")
        require(isinstance(node_id, str) and bool(node_id))
        require(isinstance(agent_token, str) and bool(agent_token))
        nodes = self.nodes(auth)
        require(len(nodes) == 1 and nodes[0].get("uuid") == node_id)
        original = nodes[0]
        require(original.get("token") == agent_token and bool(original.get("created_at")))
        self.passed("synthetic-node-api-creation")

        self.stage = "v2-http-agent-report"
        for cpu in (10, 25):
            self.agent_http(agent_token, cpu)
            time.sleep(1.1)
        self.passed("v2-http-agent-report-two-samples")

        self.stage = "v2-websocket-agent-exchange"
        websocket = WebSocket(self.port, "/api/clients/v2/rpc?token=" + quote(agent_token, safe=""))
        try:
            result = websocket.rpc("agent.report", {"report": self.report(40)}, 3)
            require(isinstance(result, dict) and result.get("status") == "success")
            result = websocket.rpc("agent.pull", {}, 4)
            require(isinstance(result, dict) and isinstance(result.get("events"), list))
        finally:
            websocket.close()
        self.passed("v2-websocket-agent-report-and-pull")

        self.stage = "recent-reports-and-public-version-rpc"
        recent = self.successful("/api/recent/" + quote(node_id, safe=""), headers=auth)
        require(isinstance(recent, list))
        require(all(cpu in [record.get("cpu", {}).get("usage") for record in recent]
                    for cpu in (10, 25, 40)))
        require(all(record.get("uuid") == node_id and bool(record.get("updated_at"))
                    for record in recent))
        require(self.rpc("public:getVersion", {}, auth) == initial_version)
        self.passed("recent-reports-api")
        self.passed("public-version-json-rpc")

        self.stage = "raw-and-legacy-history-before-restart"
        self.wait_for(lambda: self.raw_samples(node_id, auth, (10, 25, 40)))
        self.passed("fresh-raw-cpu-history")
        original_history = self.wait_for(lambda: self.legacy_history(node_id, auth))
        self.passed("legacy-load-history-api")

        self.stage = "unauthorized-admin-and-agent-rejection"
        for headers in ({}, {"Authorization": "Bearer " + secrets.token_hex(32)}):
            status, _, _ = self.request("/api/admin/client/list", headers=headers)
            require(status in (401, 403))
            status, _, _ = self.request("/api/admin/client/add", {"name": "must-not-exist"}, headers)
            require(status in (401, 403))
        status, _, _ = self.request("/api/clients/v2/rpc", {
            "token": secrets.token_hex(32), "jsonrpc": "2.0", "id": 9,
            "method": "agent.report", "params": {"report": self.report(99)},
        })
        require(status in (401, 403) and len(self.nodes(auth)) == 1)
        self.passed("unauthorized-admin-read-write-and-agent-rejection")

        self.stage = "restart-persistence"
        require(self.stop())
        self.start()
        require(self.wait_for(self.version) == initial_version)
        nodes = self.nodes(auth)
        require(len(nodes) == 1)
        for name in ("uuid", "token", "created_at", "name"):
            require(nodes[0].get(name) == original.get(name))
        settings = self.settings(auth)
        require(settings.get("sitename") == site_name)
        require(settings.get("api_key") == api_key)
        require(settings.get("notification_enabled") is False)
        self.passed("restart-node-id-token-created-at-persistence")
        self.passed("restart-settings-and-custom-sitename-persistence")
        self.passed("restart-legacy-and-product-version-persistence")

        self.stage = "restart-metric-history-persistence"
        # Komari intentionally keeps exact raw samples only in memory. Durable
        # minute aggregates survive restart; replacing the missing raw samples
        # with aggregates would misrepresent the protocol's freshness contract.
        require(self.raw_samples(node_id, auth, ()) == [])
        self.passed("restart-exact-raw-window-empty-by-design")
        restored_history = self.wait_for(lambda: self.legacy_history(node_id, auth))
        history_identity = lambda records: sorted(
            (record.get("client"), record.get("time"), record.get("cpu"))
            for record in records
        )
        require(history_identity(restored_history) == history_identity(original_history))
        self.passed("restart-legacy-history-persistence")
        self.agent_http(agent_token, 35)
        self.wait_for(lambda: self.raw_samples(node_id, auth, (35,)))
        self.passed("restart-existing-agent-token-accepted")
        self.passed("restart-new-raw-samples-readable")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path,
                        help="absolute path to a built ShellBeam Server binary")
    args = parser.parse_args()
    if not args.binary.is_absolute():
        parser.error("--binary must be an absolute path")
    try:
        binary = args.binary.resolve(strict=True)
        require(binary.is_file() and os.access(binary, os.X_OK))
    except (OSError, AssertionError):
        parser.error("--binary must identify an existing executable file")

    smoke = None
    failure = None
    cleanup = {"server_stopped": True, "temporary_state_removed": False}
    temp = tempfile.TemporaryDirectory(prefix="shellbeam-server-compat-")
    root = Path(temp.name)
    os.chmod(root, 0o700)
    handlers = {}

    def interrupted(_signum, _frame):
        raise InterruptedError("interrupted")

    try:
        for signum in (signal.SIGINT, signal.SIGTERM):
            handlers[signum] = signal.signal(signum, interrupted)
        log_fd = os.open(str(root / "server.log"), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(log_fd, "wb") as log:
            try:
                smoke = Smoke(binary, root, log)
                smoke.run()
            except (Exception, KeyboardInterrupt) as error:
                failure = {"stage": smoke.stage if smoke else "initialization",
                           "type": type(error).__name__}
            finally:
                if smoke is not None:
                    try:
                        cleanup["server_stopped"] = smoke.stop()
                    except Exception as error:
                        cleanup["server_stopped"] = False
                        failure = failure or {"stage": "cleanup-stop", "type": type(error).__name__}
    finally:
        for signum, handler in handlers.items():
            signal.signal(signum, handler)
        if cleanup["server_stopped"]:
            try:
                temp.cleanup()
                cleanup["temporary_state_removed"] = not root.exists()
                if not cleanup["temporary_state_removed"]:
                    failure = failure or {"stage": "cleanup-directory", "type": "OSError"}
            except OSError as error:
                failure = failure or {"stage": "cleanup-directory", "type": type(error).__name__}
        else:
            # Retain private state only if the OS did not confirm process exit.
            temp._finalizer.detach()

    checks = smoke.checks if smoke else []
    summary = {"result": "failed" if failure else "passed", "checks": checks,
               "count": len(checks), "cleanup": cleanup}
    if failure:
        summary["failure"] = failure
    print(json.dumps(summary, separators=(",", ":")))
    return 1 if failure else 0


if __name__ == "__main__":
    sys.exit(main())
