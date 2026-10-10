#!/usr/bin/env python3
"""Spec 021 wire QA: full snapshot cycle on empty_catalog_test (disposable).

Fleet-mode stdio session against bin/pdfm-mcp. Prints a one-line summary per
call; writes the raw transcript and the server stderr for the leak audit.
"""
import json, os, subprocess, sys, threading, time

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
BIN = os.path.join(ROOT, "bin", "pdfm-mcp")
VM = "empty_catalog_test"
PW = "qa-wire-2026-X"
OUT = os.path.join(ROOT, "docs/qa/021-wire-transcript.jsonl")
ERR = os.path.join(ROOT, "docs/qa/021-wire-server.stderr")

proc = subprocess.Popen(
    [BIN], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
    stderr=open(ERR, "wb"), text=True, bufsize=1, cwd=ROOT)

resp = {}
def reader():
    for line in proc.stdout:
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            m = json.loads(line)
        except json.JSONDecodeError:
            continue
        if "id" in m:
            resp[m["id"]] = m

t = threading.Thread(target=reader, daemon=True)
t.start()

calls = []
def call(name, args=None, expect_error=None):
    cid = len(calls) + 1
    calls.append((cid, name, args))
    logged = dict(args or {})
    if "password" in logged:
        logged["password"] = "***"
    if "current_password" in logged:
        logged["current_password"] = "***"
    if "new_password" in logged:
        logged["new_password"] = "***"
    with open(OUT, "a") as f:
        f.write(json.dumps({"id": cid, "name": name, "args": logged}) + "\n")
    proc.stdin.write(json.dumps({"jsonrpc": "2.0", "id": cid, "method": "tools/call",
                                 "params": {"name": name, "arguments": args or {}}}) + "\n")
    proc.stdin.flush()
    deadline = time.time() + 240
    while cid not in resp and time.time() < deadline:
        time.sleep(0.2)
    if cid not in resp:
        print(f"[{cid}] {name}: TIMEOUT")
        return None
    m = resp[cid]
    with open(OUT, "a") as f:
        f.write(json.dumps(m) + "\n")
    r = m.get("result", {})
    sc = r.get("structuredContent", {})
    err = r.get("isError", False)
    text = ""
    for c in r.get("content", []):
        if c.get("type") == "text":
            text = c.get("text", "")[:160]
    summary = f"[{cid}] {name}: isError={err}"
    if expect_error:
        ok = err and expect_error in (text or json.dumps(sc))
        summary += f" EXPECTED-ERROR({'match' if ok else 'MISMATCH! ' + text})"
        if not ok:
            summary += f" SC={json.dumps(sc)[:200]}"
    print(summary)
    return sc if not err else None

# initialize
proc.stdin.write(json.dumps({"jsonrpc": "2.0", "id": 0, "method": "initialize",
    "params": {"protocolVersion": "2025-06-18", "capabilities": {},
               "clientInfo": {"name": "qa", "version": "0"}}}) + "\n")
proc.stdin.flush()
deadline = time.time() + 30
while 0 not in resp and time.time() < deadline:
    time.sleep(0.1)
print("[0] initialize:", "ok" if 0 in resp else "TIMEOUT")
if 0 not in resp:
    sys.exit(1)
proc.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
proc.stdin.flush()

def info_state():
    sc = call("parallels_vm_info", {"vm_id": VM})
    if sc is None:
        return None
    vm = sc.get("vm", {})
    return vm.get("State"), vm.get("Security", {}).get("Encrypted")

st = info_state()
print("    baseline state/encrypted:", st)

sc = call("parallels_vm_snapshot_list", {"vm_id": VM})
print("    empty list:", sc.get("snapshots") if sc else None)

sc = call("parallels_vm_snapshot_create", {"vm_id": VM, "name": "qa-wire-1"})
id1 = sc.get("snapshot_id") if sc else None
print("    created:", id1)

sc = call("parallels_vm_snapshot_create", {"vm_id": VM, "name": "qa-wire-2", "description": "wire qa two: colon"})
id2 = sc.get("snapshot_id") if sc else None
print("    created:", id2)

sc = call("parallels_vm_snapshot_list", {"vm_id": VM})
snaps = sc.get("snapshots", []) if sc else []
a = [s for s in snaps if s["id"] == id1]
b = [s for s in snaps if s["id"] == id2]
print("    list:", [(s["parent"] == "", s["current"]) for s in a],
      [(s["parent"] == id1, s["current"]) for s in b])

sc = call("parallels_vm_snapshot_list", {"vm_id": VM, "snapshot_id": id2})
s = sc.get("snapshot", {}) if sc else {}
print("    single:", s.get("id") == id2, "desc=", repr(s.get("description")), "state=", s.get("state"))

sc = call("parallels_vm_snapshot_list", {"vm_id": VM, "tree": True})
print("    tree:", repr((sc or {}).get("output", "")[:80]))

sc = call("parallels_vm_snapshot_switch", {"vm_id": VM, "snapshot_id": id1})
print("    switch ok:", sc is not None)
st = info_state()
print("    state after switch (poweroff snap):", st)
sc = call("parallels_vm_snapshot_list", {"vm_id": VM})
print("    current now:", [s["id"] == id1 for s in sc.get("snapshots", []) if s["current"]])

sc = call("parallels_vm_snapshot_delete", {"vm_id": VM, "snapshot_id": id2})
print("    delete child ok:", sc is not None)
sc = call("parallels_vm_snapshot_delete", {"vm_id": VM, "snapshot_id": id1})
print("    delete current ok:", sc is not None)
sc = call("parallels_vm_snapshot_list", {"vm_id": VM})
print("    list after all deletes:", sc.get("snapshots") if sc else None)

# Bogus id: delete must surface prlctl's error (exit 255), not a silent success.
call("parallels_vm_snapshot_delete", {"vm_id": VM, "snapshot_id": "{00000000-0000-0000-0000-000000000000}"}, expect_error="Unable to find the snapshot")

# Encrypted cycle: password over the wire (stdin to prlctl).
sc = call("parallels_vm_encrypt", {"vm_id": VM, "password": PW})
print("    encrypt ok:", sc is not None)
st = info_state()
print("    encrypted:", st)

call("parallels_vm_snapshot_list", {"vm_id": VM}, expect_error="authorise")
sc = call("parallels_vm_snapshot_list", {"vm_id": VM, "password": PW})
print("    list with password:", sc.get("snapshots") if sc else None)

sc = call("parallels_vm_snapshot_create", {"vm_id": VM, "name": "qa-enc", "password": PW})
id3 = sc.get("snapshot_id") if sc else None
print("    created on encrypted:", id3)
sc = call("parallels_vm_snapshot_delete", {"vm_id": VM, "snapshot_id": id3, "password": PW})
print("    delete on encrypted ok:", sc is not None)
sc = call("parallels_vm_snapshot_list", {"vm_id": VM, "password": PW})
print("    list on encrypted after delete:", sc.get("snapshots") if sc else None)

sc = call("parallels_vm_decrypt", {"vm_id": VM, "password": PW})
print("    decrypt ok:", sc is not None)
st = info_state()
print("    FINAL state/encrypted:", st)

proc.stdin.close()
time.sleep(2)
if proc.poll() is None:
    proc.terminate()
    time.sleep(2)
    if proc.poll() is None:
        proc.kill()
print("server exit:", proc.wait())
