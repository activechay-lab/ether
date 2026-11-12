import http.server
import json
import os
import socketserver
import tempfile
import threading
import unittest

import ether_plugin
from ether_plugin import (
    CredentialError,
    PAIR_EXCHANGE_PATH,
    TUNNEL_PATH,
    clear_saved_credential,
    no_credential_message,
    pair_url_from_base,
    resolve_credential,
    tunnel_url_from_base,
    write_saved_credential,
)


class _PairHandler(http.server.BaseHTTPRequestHandler):
    calls = []

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length", "0") or "0")
        body = json.loads(self.rfile.read(length) or b"{}")
        _PairHandler.calls.append((self.path, body.get("code")))
        payload = json.dumps({"credential": "cred-1", "pluginId": "p"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *args):  # noqa: A002
        return


class _Server(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


class PairingTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = _Server(("127.0.0.1", 0), _PairHandler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base = f"http://127.0.0.1:{cls.server.server_address[1]}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()

    def setUp(self):
        _PairHandler.calls = []
        self.dir = tempfile.mkdtemp()

    def test_exchange_saves_file_with_0600(self):
        cred, from_file, state_dir = resolve_credential(
            "p", url=self.base, code="CODE-1", state_dir=self.dir
        )
        self.assertEqual(cred, "cred-1")
        self.assertTrue(from_file)
        self.assertEqual(_PairHandler.calls, [(PAIR_EXCHANGE_PATH, "CODE-1")])
        path = os.path.join(self.dir, "credential")
        self.assertEqual(os.stat(path).st_mode & 0o777, 0o600)
        self.assertEqual(os.stat(self.dir).st_mode & 0o777, 0o700)

    def test_restart_uses_file_without_code(self):
        resolve_credential("p", url=self.base, code="CODE-1", state_dir=self.dir)
        cred, from_file, _ = resolve_credential("p", state_dir=self.dir)
        self.assertEqual(cred, "cred-1")
        self.assertTrue(from_file)
        self.assertEqual(len(_PairHandler.calls), 1)

    def test_explicit_credential_wins(self):
        write_saved_credential(self.dir, "saved", self.base)
        cred, from_file, _ = resolve_credential(
            "p", credential="explicit", url=self.base, code="CODE-1", state_dir=self.dir
        )
        self.assertEqual(cred, "explicit")
        self.assertFalse(from_file)
        self.assertEqual(_PairHandler.calls, [])

    def test_no_credential_gives_clear_line(self):
        with self.assertRaises(CredentialError) as ctx:
            resolve_credential("p", state_dir=self.dir)
        self.assertEqual(str(ctx.exception), no_credential_message())

    def test_url_derivation(self):
        self.assertEqual(
            tunnel_url_from_base("http://host:32450"),
            "ws://host:32450" + TUNNEL_PATH,
        )
        self.assertEqual(
            tunnel_url_from_base("https://host/"),
            "wss://host" + TUNNEL_PATH,
        )
        self.assertEqual(pair_url_from_base("https://host"), "https://host" + PAIR_EXCHANGE_PATH)
        with self.assertRaises(CredentialError):
            tunnel_url_from_base("ftp://host")


if __name__ == "__main__":
    unittest.main()
