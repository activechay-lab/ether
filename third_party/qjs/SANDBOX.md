# Ether's qjs fork

This is the Go runtime and embedded QuickJS WASM from fastschema/qjs v0.0.6
(MIT; see LICENSE). Ether removes the WASI directory mount and cwd lookup,
sets default streams to discard, and never supplies WASI environment or args.
Ether passes plugin output to its logger. No host directory is preopened, so
there is no temporary directory or cleanup, and Windows has the same boundary.
Do not restore the upstream mount, even with a supposedly read-only host path.

The embedded WASM is patched to omit std, os and bjson registration and the
helper that installs them as globals, both in New_QJSContext and in its
inlined copy in New_QJS. `patch-wasm.py` reproduces it from the pinned upstream
binary (also retained as a test fixture), verifying input and output hashes.
Upgrading qjs requires revisiting this patch and the sandbox regression tests;
function numbers are specific to this exact upstream binary. The upstream binary
is used only in tests to verify the no-filesystem boundary independently.
