#!/usr/bin/env python3
"""Real subprocess/TCP/TLS pause, SIGKILL, restart/resume test. Localhost only.
Requires Python 3 and openssl; no third-party Python packages.
"""
import argparse
import csv
import io
import json
import pathlib
import signal
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--binary', default='release_assets/ipdata-web')
    ap.add_argument('--report', default='test-artifacts/process-e2e.json')
    args = ap.parse_args()
    binary = str(pathlib.Path(args.binary).resolve())
    proc = None
    fixtures = []
    counts = {}
    guard = threading.Lock()
    with tempfile.TemporaryDirectory(prefix='ipdata-e2e-') as temp:
        work = pathlib.Path(temp)
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-keyout', str(work / 'key.pem'), '-out', str(work / 'cert.pem'),
                        '-days', '1', '-subj', '/CN=alpha.example'],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass
            def do_GET(self):
                with guard:
                    k = str(self.server.server_port) + ':' + self.headers.get('Host', '')
                    counts[k] = counts.get(k, 0) + 1
                try:
                    self.send_response(200)
                    if self.path == '/download':
                        self.send_header('Content-Type', 'application/octet-stream')
                        self.send_header('Content-Length', '32768')
                        self.end_headers()
                        for _ in range(4):
                            self.wfile.write(bytes(8192))
                            self.wfile.flush()
                            time.sleep(.02)
                    else:
                        host = self.headers['Host']
                        body = f'fl=123\ncolo=LAX\nloc=US\nh={host}\nip=203.0.113.10\n'.encode()
                        self.send_header('Server', 'cloudflare')
                        self.send_header('Content-Length', str(len(body)))
                        self.end_headers()
                        self.wfile.write(body)
                except (BrokenPipeError, ConnectionResetError, ssl.SSLError):
                    pass
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(work / 'cert.pem', work / 'key.pem')
        for _ in range(8):
            srv = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
            srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
            fixtures.append(srv)
            threading.Thread(target=srv.serve_forever, daemon=True).start()
        sock = socket.socket()
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
        sock.close()
        base = f'http://127.0.0.1:{port}'
        def request(path, body=None):
            data = None if body is None else json.dumps(body).encode()
            req = urllib.request.Request(base + path, data=data,
                                         headers={'Content-Type': 'application/json'})
            with urllib.request.urlopen(req, timeout=10) as res:
                return res.read()
        def api(path, body=None):
            return json.loads(request(path, body))
        def launch():
            nonlocal proc
            proc = subprocess.Popen([binary, '-listen', f'127.0.0.1:{port}',
                                     '-data', str(work / 'tasks')],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            for _ in range(150):
                if proc.poll() is not None:
                    raise RuntimeError('server exited during startup')
                try:
                    api('/api/tasks')
                    return
                except OSError:
                    time.sleep(.02)
            raise TimeoutError('server startup')
        def wait_task(id, predicate):
            for _ in range(500):
                response = api('/api/tasks/' + id + '?limit=1000')
                if predicate(response['task']):
                    return response
                time.sleep(.02)
            raise TimeoutError('task status: ' + str(response['task']))
        try:
            launch()
            config = dict(targets='127.0.0.1', ports=','.join(str(s.server_port) for s in fixtures),
                          sni='alpha.example', verify_sni='beta.example', concurrency=1,
                          rate=5, timeout_ms=2000, rounds=1, insecure=True, provider='local-fixture',
                          speed_url='https://download.example/download', speed_bytes=32768,
                          speed_timeout_ms=2000)
            task = api('/api/tasks', dict(name='Real process restart test', config=config))
            id = task['id']
            api('/api/tasks/' + id + '/resume', {})
            wait_task(id, lambda t: t['completed'] >= 1)
            api('/api/tasks/' + id + '/pause', {})
            paused = wait_task(id, lambda t: t['status'] == 'paused')
            assert 0 < paused['task']['completed'] < 8
            api('/api/tasks/' + id + '/resume', {})
            before_kill = wait_task(id, lambda t: t['completed'] > paused['task']['completed'])
            assert before_kill['task']['status'] == 'running'
            proc.kill()
            proc.wait(timeout=5)
            launch()
            recovered = api('/api/tasks/' + id + '?limit=1000')
            assert recovered['task']['status'] == 'paused', recovered
            assert recovered['task']['completed'] >= before_kill['task']['completed']
            completed_before_restart = list(recovered['results'])
            api('/api/tasks/' + id + '/resume', {})
            final = wait_task(id, lambda t: t['status'] == 'completed')
            assert final['task']['completed'] == final['task']['hits'] == 8
            assert len({(r['ip'], r['port']) for r in final['results']}) == 8
            for r in final['results']:
                assert r['evidence'] == 'dual_verified', r
                assert r['speed']['bytes'] == 32768 and not r['speed'].get('error'), r
            with guard:
                for r in completed_before_restart:
                    for host in ['alpha.example', 'beta.example', 'download.example']:
                        assert counts[f'{r["port"]}:{host}'] == 1, counts
            exported = request('/api/tasks/' + id + '/export?format=csv').decode('utf-8-sig')
            rows = list(csv.DictReader(io.StringIO(exported)))
            assert len(rows) == 8 and all(float(r['speed_mbps']) > 0 for r in rows)
            report = dict(scope='controlled localhost TCP/TLS; no public target requests',
                          paused_completed=paused['task']['completed'],
                          before_sigkill=before_kill['task']['completed'],
                          recovered_completed=recovered['task']['completed'],
                          final_completed=8, dual_verified=8, speed_download_bytes=8*32768,
                          completed_targets_repeated=False, csv_rows=len(rows), passed=True)
            pathlib.Path(args.report).parent.mkdir(parents=True, exist_ok=True)
            pathlib.Path(args.report).write_text(json.dumps(report, indent=2) + '\n')
            print(json.dumps(report, indent=2))
        finally:
            if proc is not None and proc.poll() is None:
                proc.send_signal(signal.SIGTERM)
                proc.wait(timeout=10)
            for server in fixtures:
                server.shutdown()
                server.server_close()

if __name__ == '__main__':
    main()
