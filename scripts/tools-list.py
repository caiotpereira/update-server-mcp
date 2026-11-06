#!/usr/bin/env python3
# Copyright (c) 2026 Caio Pereira
# SPDX-License-Identifier: MIT
# List the tools a server advertises. A plain printf pipe does not work:
# stdin must stay open until the reply arrives.
import json,subprocess,sys,time
def count(b):
    p=subprocess.Popen([b],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True)
    for m in ['{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}','{"jsonrpc":"2.0","method":"notifications/initialized"}','{"jsonrpc":"2.0","id":2,"method":"tools/list"}']:
        p.stdin.write(m+"\n"); p.stdin.flush()
    names=[]
    while True:
        l=p.stdout.readline()
        if not l: break
        d=json.loads(l)
        if d.get("id")==2:
            names=[t["name"] for t in d["result"]["tools"]]; break
    p.kill()
    print(b, len(names), " ".join(sorted(names)))
for b in sys.argv[1:]: count(b)
