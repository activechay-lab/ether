#!/usr/bin/env python3
"""Reproduce Ether's qjs.wasm from the unmodified qjs v0.0.6 binary.

Requires wabt 1.0.42's wasm2wat and wat2wasm. Usage: python3 patch-wasm.py UPSTREAM_WASM
"""
import hashlib
import pathlib
import subprocess
import sys
import tempfile

original = pathlib.Path(sys.argv[1])
assert hashlib.sha256(original.read_bytes()).hexdigest() == "88fcbeead33cca3ec8394feb0fb3eec8047ec9ac5062aba7a63799cee11989b7"
with tempfile.TemporaryDirectory() as tmp:
    wat = pathlib.Path(tmp) / "qjs.wat"
    subprocess.run(["wasm2wat", str(original), "-o", str(wat)], check=True)
    text = wat.read_text()
    # The optimizer inlines New_QJSContext into New_QJS, hence two sites.
    calls = """    call 1093
    local.get 0
    call 1095
    local.get 0
    call 1103
    local.get 0
    call 42
    local.get 0"""
    assert text.count(calls) == 2
    # Suppress the registrations (std, os, bjson) and js_set_global_objs,
    # which imports those modules and installs their globals. Keep the context.
    text = text.replace(calls, "")
    # The context was left on the stack by local.tee; avoid an extra value.
    text = text.replace("    local.tee 0\n\n)", "    local.tee 0)")
    wat.write_text(text)
    output = pathlib.Path(__file__).with_name("qjs.wasm")
    subprocess.run(["wat2wasm", str(wat), "-o", str(output)], check=True)
    assert hashlib.sha256(output.read_bytes()).hexdigest() == "1dd090ffc9297bb0c994c091e88e2f9063fe6ec63317fe7b0bdb79e9aabe4d37"
