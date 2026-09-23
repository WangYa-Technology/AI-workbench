#!/usr/bin/env python3
"""Exercise the checked-in Nginx upload boundary using disposable containers.

Requires Docker and locally available proxy/Node images. No application,
database, credentials, or external provider is used. The upstream only hashes
request bytes, so this checks proxy transport, not API authorization/scanning.
"""

import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import tempfile
import time
import unittest
import uuid


ROOT = Path(__file__).resolve().parents[1]
MIB = 1 << 20
UPLOAD_ENVELOPE = 10 * MIB + (256 << 10)
DOWNLOAD_SIZE = 64 * MIB
ASSET_ID = "00000000-0000-4000-8000-000000000001"


def docker(*args, combine_output=False):
    result = subprocess.run(
        ["docker", *args], capture_output=True, text=True, timeout=60
    )
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or result.stdout.strip())
    output = result.stdout + result.stderr if combine_output else result.stdout or result.stderr
    return output.strip()


class ProductionProxyTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        proxy_image = os.environ.get("HCAI_PROXY_TEST_IMAGE", "hcai-chat-web:local")
        node_image = os.environ.get("HCAI_PROXY_FIXTURE_IMAGE", "node:24-alpine")
        # Never pull unexpectedly; CI/release checks prepare images explicitly.
        docker("image", "inspect", proxy_image)
        docker("image", "inspect", node_image)
        model = json.loads(docker(
            "compose", "--env-file", str(ROOT / "deploy/.env.production.example"),
            "-f", str(ROOT / "deploy/compose.production.yml"),
            "config", "--no-env-resolution", "--format", "json",
        ))
        web = model["services"]["web"]
        if not web["read_only"] or web["cap_drop"] != ["ALL"]:
            raise RuntimeError("Production web must remain read-only and drop capabilities")
        mounts = [arg for mount in web["tmpfs"] for arg in ["--tmpfs", mount]]
        security = [arg for value in web["security_opt"] for arg in ["--security-opt", value]]
        suffix = uuid.uuid4().hex[:12]
        network = "hcai-proxy-test-" + suffix
        cls.proxy = network + "-web"
        upstream = network + "-upstream"
        directory = tempfile.TemporaryDirectory(prefix="hcai-proxy-test-")
        cls.addClassCleanup(directory.cleanup)
        script = Path(directory.name) / "upstream.mjs"
        script.write_text(r"""
import http from 'node:http';
import {createHash} from 'node:crypto';
const mediaBytes = 64 * 1024 * 1024;
let latestStream = {bytes: 0, finished: false, closed: false};
http.createServer((req, res) => {
  req.on('error', () => {}); // Expected when the proxy rejects an in-flight body.
  if (req.url.startsWith('/api/proxy-failure')) { req.destroy(); return; }
  if (req.url === '/api/proxy-progress') {
    res.writeHead(200, {'Content-Type': 'application/json'});
    res.end(JSON.stringify(latestStream));
    return;
  }
  const path = req.url.split('?')[0];
  if ((path.startsWith('/api/v1/assets/') || path.startsWith('/api/v1/admin/products/')) && path.endsWith('/content')) {
    let first = 0, last = mediaBytes - 1, status = 200;
    const headers = {'Content-Type': 'application/octet-stream', 'Cache-Control': 'private, no-store',
      'Accept-Ranges': 'bytes', 'ETag': '"synthetic-private"'};
    if (req.headers.range) {
      const range = /^bytes=(\d+)-(\d+)$/.exec(req.headers.range);
      if (!range || Number(range[1]) > Number(range[2]) || Number(range[2]) >= mediaBytes) {
        res.writeHead(416); res.end(); return;
      }
      first = Number(range[1]); last = Number(range[2]); status = 206;
      headers['Content-Range'] = `bytes ${first}-${last}/${mediaBytes}`;
    }
    const size = last - first + 1;
    headers['Content-Length'] = String(size);
    res.writeHead(status, headers);
    const progress = {bytes: 0, finished: false, closed: false};
    latestStream = progress;
    res.on('finish', () => { progress.finished = true; });
    res.on('close', () => { progress.closed = true; });
    const chunk = Buffer.alloc(64 * 1024, 'P');
    function pump() {
      while (!res.destroyed && progress.bytes < size) {
        const part = chunk.subarray(0, Math.min(chunk.length, size - progress.bytes));
        progress.bytes += part.length;
        if (!res.write(part)) { res.once('drain', pump); return; }
      }
      if (!res.destroyed) res.end();
    }
    pump();
    return;
  }
  let bytes = 0;
  const hash = createHash('sha256');
  req.on('data', chunk => { bytes += chunk.length; hash.update(chunk); });
  req.on('end', () => {
    res.writeHead(200, {'Content-Type': 'application/json'});
    res.end(JSON.stringify({bytes, sha256: hash.digest('hex'),
      method: req.method, path: req.url, type: req.headers['content-type'],
      key: req.headers['idempotency-key'], requestId: req.headers['x-request-id'],
      forwardedFor: req.headers['x-forwarded-for'], realIP: req.headers['x-real-ip'],
      origin: req.headers.origin, fetchSite: req.headers['sec-fetch-site']}));
  });
}).listen(8080, '0.0.0.0');
""")
        os.chmod(directory.name, 0o755)
        os.chmod(script, 0o644)
        # A dedicated bridge permits a loopback-only published client port.
        # The fixture makes no outgoing requests and has no credentials.
        docker("network", "create", network)
        cls.addClassCleanup(docker, "network", "rm", network)
        for name in [upstream, cls.proxy]:
            cls.addClassCleanup(cls.remove_container, name)
        docker(
            "run", "--pull=never", "-d", "--name", upstream,
            "--network", network, "--network-alias", "api", "--user", "node",
            "--read-only", "--cap-drop=ALL", "--security-opt", "no-new-privileges",
            "--mount", f"type=bind,src={script},dst=/upstream.mjs,readonly",
            "--entrypoint", "node", node_image, "/upstream.mjs",
        )
        docker(
            "run", "--pull=never", "-d", "--name", cls.proxy,
            "--network", network, "--publish", "127.0.0.1::8080",
            "--read-only", "--cap-drop=ALL", *security, *mounts,
            "--mount", f"type=bind,src={ROOT / 'deploy/nginx.conf'},dst=/etc/nginx/nginx.conf,readonly",
            proxy_image, "nginx", "-g", "daemon off;",
        )
        try:
            cls.port = int(docker("port", cls.proxy, "8080/tcp").rsplit(":", 1)[1])
        except RuntimeError as error:
            raise RuntimeError(f"{error}\n{docker('logs', cls.proxy)}") from error
        for _ in range(100):
            try:
                if cls.send("GET", "/health")[0] == 200:
                    break
            except (OSError, http.client.HTTPException):
                pass
            time.sleep(0.1)
        else:
            raise RuntimeError("Disposable proxy did not become ready: " + docker("logs", cls.proxy))
        version = subprocess.run(["docker", "exec", cls.proxy, "nginx", "-v"],
                                 capture_output=True, text=True, timeout=30)
        version_text = (version.stdout + version.stderr).strip()
        print(version_text)
        expected = re.search(r"^FROM nginx:(\d+\.\d+\.\d+)-alpine(?:\s|$)",
                             (ROOT / "deploy/Dockerfile.web").read_text(), re.MULTILINE)
        actual = re.search(r"nginx/(\d+\.\d+\.\d+)(?:\s|$)", version_text)
        if version.returncode or expected is None or actual is None or actual.group(1) != expected.group(1):
            raise RuntimeError("Proxy test image must use the Nginx patch version declared in deploy/Dockerfile.web")

    @staticmethod
    def remove_container(name):
        subprocess.run(["docker", "rm", "-f", name], capture_output=True, timeout=30)

    @classmethod
    def send(cls, method, path, body=b"", *, chunked=False, declared_size=None, extra_headers=None):
        conn = http.client.HTTPConnection("127.0.0.1", cls.port, timeout=20)
        try:
            headers = {"Content-Type": "multipart/form-data; boundary=hcai-proxy-test",
                       "Idempotency-Key": "proxy-upload-test"}
            headers.update(extra_headers or {})
            if declared_size is not None:
                # Nginx must reject oversized Content-Length before reading the body.
                conn.putrequest(method, path)
                conn.putheader("Content-Length", str(declared_size))
                for name, value in (extra_headers or {}).items():
                    conn.putheader(name, value)
                conn.endheaders()
            else:
                payload = (body[i:i + 65536] for i in range(0, len(body), 65536)) if chunked else body
                conn.request(method, path, payload, headers, encode_chunked=chunked)
            response = conn.getresponse()
            return response.status, response.read()
        finally:
            conn.close()

    def test_full_size_asset_and_version_uploads(self):
        # A file at the service's 10 MiB limit also needs multipart overhead.
        body = (b'--hcai-proxy-test\r\nContent-Disposition: form-data; name="file"; filename="source.txt"\r\n'
                b'Content-Type: text/plain\r\n\r\n' + b"a" * (10 * MIB) + b"\r\n--hcai-proxy-test--\r\n")
        for path in ["/api/v1/assets/uploads", f"/api/v1/assets/{ASSET_ID}/versions"]:
            for chunked in [False, True]:
                with self.subTest(path=path, chunked=chunked):
                    status, response = self.send("POST", path, body, chunked=chunked)
                    self.assertEqual(status, 200, response[:200])
                    result = json.loads(response)
                    self.assertEqual(result["bytes"], len(body))
                    self.assertEqual(result["sha256"], hashlib.sha256(body).hexdigest())
                    self.assertEqual(result["path"], path)
                    self.assertEqual(result["method"], "POST")
                    self.assertEqual(result["key"], "proxy-upload-test")
                    self.assertIn("boundary=hcai-proxy-test", result["type"])

    def test_upload_envelope_is_bounded(self):
        for path in ["/api/v1/assets/uploads", f"/api/v1/assets/{ASSET_ID}/versions"]:
            with self.subTest(path=path):
                self.assertEqual(self.send("POST", path, declared_size=UPLOAD_ENVELOPE + 1)[0], 413)
                self.assertEqual(self.send("POST", path, b"x" * (UPLOAD_ENVELOPE + 1), chunked=True)[0], 413)

    def test_nonroot_readonly_startup(self):
        self.assertEqual(docker("exec", self.proxy, "id", "-u"), "101")
        self.assertEqual(docker("exec", self.proxy, "id", "-g"), "101")
        state = json.loads(docker("inspect", self.proxy))[0]
        self.assertTrue(state["State"]["Running"])
        self.assertTrue(state["HostConfig"]["ReadonlyRootfs"])

    def test_unrelated_routes_keep_default_limit(self):
        for path in ["/api/v1/products", "/api/v1/assets/uploads/extra",
                     f"/api/v1/assets/{ASSET_ID}/versions/extra"]:
            with self.subTest(path=path):
                self.assertEqual(self.send("POST", path, declared_size=MIB + 1)[0], 413)

    def test_delivery_repair_keeps_its_separate_limit(self):
        path = f"/api/v1/admin/product-deliveries/{ASSET_ID}/repairs/{ASSET_ID}/content"
        body = b"r" * (11 * MIB)
        status, response = self.send("PUT", path, body)
        self.assertEqual(status, 200, response[:200])
        self.assertEqual(json.loads(response)["sha256"], hashlib.sha256(body).hexdigest())
        self.assertEqual(self.send("PUT", path, declared_size=100 * MIB + 1)[0], 413)

    def test_logs_exclude_query_credentials_and_private_headers(self):
        marker = "synthetic-sensitive-" + uuid.uuid4().hex
        headers = {"Referer": f"https://example.test/reset-password?token={marker}",
                   "Cookie": f"session={marker}", "Authorization": f"Bearer {marker}"}
        routes = [
            ("GET", "/reset-password", 200, None),
            ("POST", "/api/v1/assets/uploads", 413, UPLOAD_ENVELOPE + 1),
            ("GET", "/api/proxy-failure", 502, None),
        ]
        for method, path, expected, size in routes:
            with self.subTest(path=path):
                status, _ = self.send(method, path + "?token=" + marker,
                                      declared_size=size, extra_headers=headers)
                self.assertEqual(status, expected)
        # Include both stdout (access) and stderr (request diagnostics).
        log = docker("logs", self.proxy, combine_output=True)
        self.assertNotIn(marker, log, "Proxy logs persisted a synthetic query/header credential")
        records = []
        for line in log.splitlines():
            if line.startswith('{"kind":"http_access"'):
                records.append(json.loads(line))
        for method, path, status, _ in routes:
            self.assertTrue(any(r["method"] == method and r["path"] == path and int(r["status"]) == status
                                for r in records), f"Missing safe access result for {path}: {status}")

    def test_email_action_page_does_not_forward_referrer(self):
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
        try:
            conn.request("GET", "/reset-password?token=synthetic-referrer-test")
            response = conn.getresponse()
            response.read()
            self.assertEqual(response.status, 200)
            self.assertEqual(response.getheader("Referrer-Policy"), "no-referrer")
        finally:
            conn.close()

    def test_generated_request_id_correlates_with_upstream(self):
        supplied = "synthetic-client-id-" + uuid.uuid4().hex
        status, response = self.send("GET", "/api/v1/products", extra_headers={"X-Request-ID": supplied})
        self.assertEqual(status, 200)
        request_id = json.loads(response)["requestId"]
        self.assertRegex(request_id, r"^[a-f0-9]{32}$")
        self.assertNotEqual(request_id, supplied)
        log = docker("logs", self.proxy, combine_output=True)
        self.assertNotIn(supplied, log)
        records = [json.loads(line) for line in log.splitlines() if line.startswith('{"kind":"http_access"')]
        self.assertTrue(any(r["requestId"] == request_id and r["path"] == "/api/v1/products"
                            and int(r["status"]) == 200 for r in records))

    def test_forwarded_chain_appends_observed_client(self):
        # The API must walk this chain right-to-left: the prefix is untrusted
        # caller input, even though the immediate TCP peer is our Nginx.
        forged = "203.0.113.8, 192.0.2.9"
        for path in ["/api/v1/auth/login", "/health", "/ready"]:
            with self.subTest(path=path):
                status, response = self.send("GET", path, extra_headers={
                    "X-Forwarded-For": forged, "X-Real-IP": "203.0.113.8",
                })
                self.assertEqual(status, 200)
                result = json.loads(response)
                observed = result["realIP"]
                ipaddress.ip_address(observed)
                self.assertNotIn(observed, ["203.0.113.8", "192.0.2.9"])
                self.assertEqual(result["forwardedFor"], forged + ", " + observed)

    def test_browser_origin_metadata_reaches_api(self):
        for origin, site in [("https://app.example.test", "same-origin"),
                             ("https://other.example.test", "same-site"),
                             ("null", "cross-site")]:
            with self.subTest(origin=origin, site=site):
                status, response = self.send("POST", "/api/v1/auth/logout", extra_headers={
                    "Origin": origin, "Sec-Fetch-Site": site,
                })
                self.assertEqual(status, 200)
                result = json.loads(response)
                self.assertEqual(result["origin"], origin)
                self.assertEqual(result["fetchSite"], site)

    def test_private_download_backpressure_without_temp_files(self):
        for prefix in ["assets", "admin/products"]:
            with self.subTest(prefix=prefix):
                conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
                try:
                    conn.connect()
                    conn.sock.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 4096)
                    conn.request("GET", f"/api/v1/{prefix}/{ASSET_ID}/content")
                    response = conn.getresponse()
                    self.assertEqual(response.status, 200)
                    self.assertEqual(int(response.getheader("Content-Length")), DOWNLOAD_SIZE)
                    self.assertEqual(response.getheader("Cache-Control"), "private, no-store")
                    # Leave the body unread: the proxy must not drain a private
                    # upstream into disk while the actual client is stalled.
                    time.sleep(1)
                    status, body = self.send("GET", "/api/proxy-progress")
                    self.assertEqual(status, 200)
                    progress = json.loads(body)
                    self.assertFalse(progress["finished"], "Proxy drained the whole private download")
                    self.assertLess(progress["bytes"], DOWNLOAD_SIZE)
                    self.assertEqual(docker("exec", self.proxy, "find", "/var/cache/nginx", "-type", "f"), "")
                    response.close()
                finally:
                    conn.close()
                for _ in range(40):
                    _, body = self.send("GET", "/api/proxy-progress")
                    if json.loads(body)["closed"]:
                        break
                    time.sleep(0.05)
                else:
                    self.fail("Client cancellation did not close the upstream download")

    def test_private_download_preserves_ranges(self):
        for prefix in ["assets", "admin/products"]:
            with self.subTest(prefix=prefix):
                conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
                try:
                    conn.request("GET", f"/api/v1/{prefix}/{ASSET_ID}/content?fileIndex=0",
                                 headers={"Range": "bytes=123-2047"})
                    response = conn.getresponse()
                    body = response.read()
                    self.assertEqual(response.status, 206)
                    self.assertEqual(body, b"P" * 1925)
                    self.assertEqual(response.getheader("Content-Length"), "1925")
                    self.assertEqual(response.getheader("Content-Range"), f"bytes 123-2047/{DOWNLOAD_SIZE}")
                    self.assertEqual(response.getheader("ETag"), '"synthetic-private"')
                    self.assertEqual(response.getheader("Cache-Control"), "private, no-store")
                finally:
                    conn.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
