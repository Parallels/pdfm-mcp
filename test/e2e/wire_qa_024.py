#!/usr/bin/env python3
"""Spec 024 wire QA: telemetry enable/disable matrix against built binaries.

The API key is baked in at build time (ldflags -X, spec 024), so this builds
two throwaway binaries in a temp dir:
  * qa-nok  — no key        => "telemetry disabled" (no API key)
  * qa-key  — throwaway key => "telemetry enabled" + a real tool call
                              produces a PDFM::PARALLELS_LIST_VMS event
                              (the expected 400 "Invalid API key" flush
                              error proves the HTTP path ran)
  qa-key + PDFM_MCP_TELEMETRY_DISABLED=true
            => "telemetry disabled" (opt-out set)
"""
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
MODULE = "github.com/Parallels/pdfm-mcp"


def build_qa_bin(name, key):
    tmp = tempfile.mkdtemp(prefix="wire-qa-024-")
    bin_path = os.path.join(tmp, name)
    ld = f"-X {MODULE}/internal/telemetry.APIKey={key}"
    subprocess.run(
        ["go", "build", "-trimpath", "-ldflags", ld, "-o", bin_path, os.path.join(ROOT, "cmd", "pdfm-mcp")],
        cwd=ROOT,
        check=True,
    )
    return bin_path, tmp


def rpc(proc, msg_id, method, params=None):
    body = json.dumps({"jsonrpc": "2.0", "id": msg_id, "method": method, "params": params or {}})
    proc.stdin.write((body + "\n").encode())
    proc.stdin.flush()
    deadline = time.time() + 20
    while time.time() < deadline:
        line = proc.stdout.readline()
        if not line:
            return None
        try:
            doc = json.loads(line)
        except Exception:
            continue
        if doc.get("id") == msg_id:
            return doc
    return None


def run_case(name, bin_path, env_extra, want_lines, tools_expected=True, call_tool=None):
    env = dict(os.environ)
    env.pop("PDFM_MCP_TELEMETRY_DISABLED", None)
    env.update(env_extra)
    env["PDFM_MCP_LOG_LEVEL"] = "info"

    proc = subprocess.Popen(
        [bin_path],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
    )
    n_tools = -1
    try:
        init = rpc(proc, 1, "initialize", {
            "protocolVersion": "2025-03-26",
            "clientInfo": {"name": "smoke", "version": "0"},
            "capabilities": {},
        })
        assert init and "result" in init, f"{name}: initialize failed: {init}"
        rpc(proc, 2, "notifications/initialized")
        tools = rpc(proc, 3, "tools/list")
        if tools and "result" in tools:
            n_tools = len(tools["result"]["tools"])
        if tools_expected and n_tools <= 0:
            raise AssertionError(f"{name}: expected tools, got {n_tools}")
        if call_tool:
            res = rpc(proc, 4, "tools/call", {"name": call_tool, "arguments": {}})
            if not res or "result" not in res:
                raise AssertionError(f"{name}: tools/call failed: {res}")
        time.sleep(1.0)
    finally:
        try:
            proc.send_signal(signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            out = proc.communicate(timeout=15)
        except subprocess.TimeoutExpired:
            proc.kill()
            out = proc.communicate(timeout=10)
    text = out[1].decode(errors="replace")
    missing = [w for w in want_lines if w not in text]
    ok = not missing
    print(f"{'PASS' if ok else 'FAIL'} {name} (tools={n_tools})")
    for line in text.splitlines():
        if "telemetry" in line or "amplitude" in line:
            print(f"    {line}")
    if missing:
        print(f"    MISSING: {missing}")
        print("    --- full stderr ---")
        print(text)
    return ok


def main():
    bin_nok, tmp_nok = build_qa_bin("qa-nok", "")
    bin_key, tmp_key = build_qa_bin("qa-key", "smoke-fake-key")
    try:
        ok1 = run_case("no key baked in => disabled", bin_nok, {}, ['telemetry disabled', 'reason="no API key"'])
        ok2 = run_case(
            "key baked in + opt-out => disabled",
            bin_key,
            {"PDFM_MCP_TELEMETRY_DISABLED": "true"},
            ['telemetry disabled', 'reason="opt-out set"'],
        )
        ok3 = run_case(
            "key baked in, no opt-out => enabled",
            bin_key,
            {},
            ["telemetry enabled", "PDFM::PARALLELS_LIST_VMS"],
            call_tool="parallels_list_vms",
        )
    finally:
        shutil.rmtree(tmp_nok, ignore_errors=True)
        shutil.rmtree(tmp_key, ignore_errors=True)
    sys.exit(0 if (ok1 and ok2 and ok3) else 1)


if __name__ == "__main__":
    main()
