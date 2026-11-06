#!/usr/bin/env python3
"""Bounded lab-only TCP forwarding. TLS remains end-to-end; no credential logging."""
import concurrent.futures
import selectors
import socket
import threading
import time

slots = threading.BoundedSemaphore(64)

def copy(client, target):
    try:
        with client, socket.create_connection(target, timeout=10) as upstream:
            client.setblocking(False)
            upstream.setblocking(False)
            with selectors.DefaultSelector() as sel:
                sel.register(client, selectors.EVENT_READ, upstream)
                sel.register(upstream, selectors.EVENT_READ, client)
                while True:
                    events = sel.select(120)
                    if not events:
                        return
                    for key, _ in events:
                        data = key.fileobj.recv(65536)
                        if not data:
                            return
                        # Bounded blocking write avoids unbounded per-peer buffers.
                        key.data.settimeout(15)
                        key.data.sendall(data)
                        key.data.setblocking(False)
    except (OSError, TimeoutError):
        pass
    finally:
        slots.release()

def listen(port, target_port):
    with socket.create_server(('0.0.0.0', port)) as server:
        while True:
            client, _ = server.accept()
            if slots.acquire(blocking=False):
                threading.Thread(target=copy, args=(client, ('ether035-mh-server', target_port)), daemon=True).start()
            else:
                client.close()

with concurrent.futures.ThreadPoolExecutor(max_workers=9) as pool:
    ports = [15432]+[base+ordinal for base in (18410,18420,18430,18440) for ordinal in (0,1)]
    list(pool.map(lambda port: listen(port, 30432 if port == 15432 else port+13000), ports))
