#!/usr/bin/env python3
"""Minimal IRR whois stub for the pathvector e2e suite.

Answers the plain whois queries pathvector's verify-irr-policy makes
(`AS<asn>` or `-s <sources> AS<asn>` over TCP/43) with a canned RPSL aut-num
object documenting peering policy with AS65001 (the e2e router). Anything
else returns "% No match", like a real IRR.

This only needs to satisfy pkg/irr/policy.go's ParsePolicy; it is not a
general IRR server.
"""
import socket
import sys
import threading

HOST = "0.0.0.0"
PORT = 43

# AS65003 is the e2e IRR-filtered peer. The aut-num documents import/export
# policy with the router (AS65001) for both v4 and v6 so VerifyPolicy passes.
AUT_NUM_65003 = """% Test IRR whois stub

aut-num:        AS65003
as-name:        TEST-IRR-PEER
import:         from AS65001 accept ANY
export:         to AS65001 announce ANY
mp-import:      afi ipv6.unicast from AS65001 accept ANY
mp-export:      afi ipv6.unicast to AS65001 announce ANY

% No IRR objects match your query
"""

# !g/!6 origin queries (used by filter-irr-origin per-prefix checks and the
# audit command). Only 185.121.172.0/24 has a route object for AS65003 — the
# peer also announces 185.121.174.0/24 which is in the as-set prefix list but
# has no route object, so the per-prefix origin filter must reject it.
ORIGIN_RESPONSES = {
    "!g65001":   "203.0.113.0/24\n",
    "!6AS65001": "2001:6464:2::/64\n",
    "!g65003":   "185.121.172.0/24\n",
    "!6AS65003": "D\n",
}


def handle(conn):
    try:
        conn.settimeout(10)
        data = b""
        while not data.endswith(b"\n") and len(data) < 4096:
            chunk = conn.recv(4096)
            if not chunk:
                break
            data += chunk
        query = data.decode(errors="replace").strip()
        print(f"whois query: {query!r}", file=sys.stderr, flush=True)
        # Strip any leading -s <sources> flag pathvector may prepend.
        q = query.split()[-1] if query else query
        if q in ORIGIN_RESPONSES:
            conn.sendall(ORIGIN_RESPONSES[q].encode())
        elif "AS65003" in query:
            conn.sendall(AUT_NUM_65003.encode())
        else:
            conn.sendall(b'% No match for "' + query.encode() + b'"\n')
    except OSError:
        pass
    finally:
        conn.close()


def main():
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind((HOST, PORT))
    srv.listen(16)
    print(f"whois stub listening on {HOST}:{PORT}", file=sys.stderr, flush=True)
    while True:
        conn, _ = srv.accept()
        threading.Thread(target=handle, args=(conn,), daemon=True).start()


if __name__ == "__main__":
    main()
