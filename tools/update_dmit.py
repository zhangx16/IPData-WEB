#!/usr/bin/env python3
"""Refresh public BGP announcement data; this script never probes target IPs."""
import concurrent.futures
import datetime
import ipaddress
import json
import pathlib
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
ASNS = (906, 32519, 54574)

def fetch(asn):
    url = f'https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS{asn}'
    req = urllib.request.Request(url, headers={'User-Agent': 'IPData-WEB/source-update'})
    with urllib.request.urlopen(req, timeout=30) as response:
        raw = response.read(10 * 1024 * 1024 + 1)
    if len(raw) > 10 * 1024 * 1024:
        raise ValueError('RIPEstat response too large')
    payload = json.loads(raw)
    if payload.get('status') != 'ok':
        raise ValueError(f'AS{asn} query failed')
    data = payload['data']
    nets = [ipaddress.ip_network(p['prefix']) for p in data['prefixes']]
    return dict(asn=asn, source=url, query_starttime=data['query_starttime'],
                query_endtime=data['query_endtime'],
                raw_v4=sum(n.version == 4 for n in nets),
                raw_v6=sum(n.version == 6 for n in nets)), nets

def main():
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        fetched = list(pool.map(fetch, ASNS))
    nets = [n for _, values in fetched for n in values]
    v4 = list(ipaddress.collapse_addresses(n for n in nets if n.version == 4))
    v6 = list(ipaddress.collapse_addresses(n for n in nets if n.version == 6))
    meta = dict(fetched_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                asns=list(ASNS), sources=[m for m, _ in fetched],
                query_starttime=min(m['query_starttime'] for m, _ in fetched),
                query_endtime=max(m['query_endtime'] for m, _ in fetched),
                raw_v4=sum(n.version == 4 for n in nets), raw_v6=sum(n.version == 6 for n in nets),
                merged_v4=len(v4), merged_v6=len(v6), v4_addresses=sum(n.num_addresses for n in v4))
    path = ROOT / 'data-sources'
    path.mkdir(exist_ok=True)
    users = path / 'dmit-user-provided.txt'
    if users.exists():
        u = list(ipaddress.collapse_addresses(ipaddress.ip_network(s) for s in users.read_text().split()))
        meta['user_addresses'] = sum(n.num_addresses for n in u)
        meta['user_not_fully_covered'] = [str(n) for n in u if
            sum(min(n.num_addresses, k.num_addresses) for k in v4 if n.overlaps(k)) < n.num_addresses]
    (path / 'dmit-ipv4.txt').write_text('\n'.join(map(str, v4)) + '\n')
    (path / 'dmit-ipv6.txt').write_text('\n'.join(map(str, v6)) + '\n')
    (path / 'dmit-source.json').write_text(json.dumps(meta, indent=2) + '\n')
    snapshot = dict(meta, ipv4=list(map(str, v4)), ipv6=list(map(str, v6)))
    (ROOT / 'combined_refactor' / 'dmit.json').write_text(json.dumps(snapshot, indent=2) + '\n')
    print(json.dumps(meta, indent=2))

if __name__ == '__main__':
    main()
