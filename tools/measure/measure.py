#!/usr/bin/env python3
# Measure todos list cold/warm/content-miss requests, GraphQL points and time.
# Requires Python 3, OpenSSL, Go (unless --binary is supplied), and network access.
# From a checkout, with GH_TOKEN or GITHUB_TOKEN already in the environment:
#   python3 tools/measure/measure.py --repo OWNER/REPO --output measurements.json
# Builds a temporary binary, uses a throwaway XDG_CACHE_HOME per state, and
# deletes temporary certificates/cache afterwards. A loopback CONNECT proxy
# adds rateLimit.cost to actual queries and refuses every non-query operation.
# Credentials are inherited by the todos child, forwarded only in memory, and
# never logged. No headers, response bodies, account names or cache keys are
# saved. Counts include proxy overhead; results depend on data and permissions.

import argparse
import collections
import http.client
import http.server
import json
import os
from pathlib import Path
import re
import ssl
import subprocess
import tempfile
import threading
import time


# This is deliberately stricter than a general GraphQL client: accept a single
# named query only. Strip strings/comments before rejecting write operations.
def instrument(query):
    clean = re.sub(r'"""[\s\S]*?"""|"(?:\\.|[^"\\])*"|#[^\n]*', '', query)
    operation = re.match(r'\s*query\s+([A-Za-z_][A-Za-z_0-9]*)\b', clean)
    if (not operation or re.search(r'\b(mutation|subscription)\b', clean)
            or len(re.findall(r'\bquery\b', clean)) != 1):
        raise ValueError('only a single named query is allowed')
    # Locate its selection set outside comments, strings and variable defaults.
    tokens = re.finditer(r'"""[\s\S]*?"""|"(?:\\.|[^"\\])*"|#[^\n]*|[(){}]', query)
    depth = 0
    for token in tokens:
        value = token.group()
        if value == '(':
            depth += 1
        elif value == ')':
            depth -= 1
        elif value == '{' and depth == 0:
            pos = token.end()
            return (query[:pos] + ' rateLimit { cost } ' + query[pos:],
                    operation.group(1))
    raise ValueError('query has no selection set')


class Counter(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, context):
        super().__init__(('127.0.0.1', 0), Proxy)
        self.context = context
        self.events = []
        self.lock = threading.Lock()

    def record(self, event):
        with self.lock:
            self.events.append(event)

    def handle_error(self, request, client_address):
        # Never print exception details or request material.
        self.record({'failure': 'proxy handler failed'})


class Proxy(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_CONNECT(self):
        if self.path != 'api.github.com:443':
            self.send_error(403)
            self.server.record({'failure': 'unexpected destination refused'})
            return
        self.send_response(200, 'Connection established')
        self.end_headers()
        wrapped = None
        upstream = None
        try:
            wrapped = self.server.context.wrap_socket(self.connection, server_side=True)
            wrapped.settimeout(45)
            with wrapped.makefile('rb') as stream:
                line = stream.readline(8192).decode('ascii').strip()
                method, path, _ = line.split(' ', 2)
                headers = http.client.parse_headers(stream)
                length = int(headers.get('Content-Length', '0'))
                if (method != 'POST' or path != '/graphql'
                        or headers.get('Transfer-Encoding') or not 0 < length <= 4 << 20):
                    raise ValueError('non-query request refused')
                payload = json.loads(stream.read(length))
                payload['query'], operation = instrument(payload['query'])
                body = json.dumps(payload).encode()
                forwarded = {k: v for k, v in headers.items() if k.lower() not in {
                    'content-length', 'accept-encoding', 'connection',
                    'proxy-connection', 'proxy-authorization', 'host'}}
                forwarded.update({'Host': 'api.github.com', 'Accept-Encoding': 'identity',
                                  'Content-Length': str(len(body))})
                upstream = http.client.HTTPSConnection('api.github.com', timeout=40,
                                                       context=ssl.create_default_context())
                upstream.request('POST', '/graphql', body=body, headers=forwarded)
                response = upstream.getresponse()
                data = response.read()
                decoded = json.loads(data)
                cost = ((decoded.get('data') or {}).get('rateLimit') or {}).get('cost')
                self.server.record({'operation': operation, 'status': response.status,
                                    'cost': cost, 'errors': len(decoded.get('errors', []))})
                output = f'HTTP/1.1 {response.status} {response.reason}\r\n'.encode()
                for key, value in response.getheaders():
                    if key.lower() not in {'transfer-encoding', 'content-length',
                                           'connection', 'content-encoding'}:
                        output += f'{key}: {value}\r\n'.encode()
                output += f'Content-Length: {len(data)}\r\nConnection: close\r\n\r\n'.encode()
                wrapped.sendall(output + data)
        except Exception:
            self.server.record({'failure': 'query refused or proxy request failed'})
        finally:
            if upstream:
                upstream.close()
            if wrapped:
                wrapped.close()
            self.close_connection = True


def measure(binary, repo, cache, env, server, state, heat):
    if heat == 'miss':
        # Remove only pages in this run's private cache; preserve structure and
        # epochs. --fresh would bypass the probe and is a different experiment.
        for entry in cache.rglob('*.json'):
            if entry.parent.name == 'content':
                entry.unlink()
    child_env = dict(env, XDG_CACHE_HOME=str(cache))
    before = len(server.events)
    start = time.monotonic()
    result = subprocess.run([str(binary), '--repo', repo, 'list', '--state', state, '--json'],
                            env=child_env, capture_output=True, text=True, timeout=90)
    elapsed = time.monotonic() - start
    events = server.events[before:]
    if result.returncode:
        raise RuntimeError(f'todos list exited {result.returncode}; output withheld')
    if not events or any('failure' in e or e.get('status') != 200
                         or e.get('errors') or not isinstance(e.get('cost'), int)
                         for e in events):
        raise RuntimeError('measurement has failed or uncounted queries')
    rows = len(json.loads(result.stdout)['issues'])
    return {'state': state, 'heat': heat, 'rows': rows, 'requests': len(events),
            'points': sum(e['cost'] for e in events), 'seconds': round(elapsed, 3),
            'operations': dict(collections.Counter(e['operation'] for e in events))}


def main():
    parser = argparse.ArgumentParser(description=__doc__ or 'Read-only CLI cache measurements')
    parser.add_argument('--repo', required=True, help='repository OWNER/REPO')
    parser.add_argument('--binary', type=Path, help='existing todos binary (default: temporary build)')
    parser.add_argument('--output', type=Path, help='save safe measurement rows as JSON')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='todos-measure-') as temporary:
        root = Path(temporary)
        binary = args.binary.resolve() if args.binary else root / 'todos'
        if not args.binary:
            subprocess.run(['go', 'build', '-o', str(binary), '.'],
                           cwd=Path(__file__).resolve().parents[2], check=True)
        key, cert = root / 'key.pem', root / 'cert.pem'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-keyout', str(key), '-out', str(cert), '-days', '1',
                        '-subj', '/CN=api.github.com', '-addext',
                        'subjectAltName=DNS:api.github.com'], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        server = Counter(context)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        env = os.environ.copy()
        env.update({'HTTPS_PROXY': f'http://127.0.0.1:{server.server_port}',
                    'https_proxy': f'http://127.0.0.1:{server.server_port}',
                    'NO_PROXY': '', 'no_proxy': '', 'SSL_CERT_FILE': str(cert),
                    'GH_HOST': 'github.com', 'TODOS_CACHE': 'on'})
        rows = []
        try:
            for state in ('all', 'closed'):
                cache = root / ('cache-' + state)
                for heat in ('cold', 'warm', 'miss'):
                    row = measure(binary, args.repo, cache, env, server, state, heat)
                    rows.append(row)
                    print(json.dumps(row), flush=True)
            if args.output:
                args.output.write_text(json.dumps(rows, indent=2) + '\n')
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.SubprocessError, OSError, ValueError) as error:
        # Do not expose subprocess output or exceptions containing request data.
        raise SystemExit(f'Measurement failed ({type(error).__name__}); no request details logged.')
