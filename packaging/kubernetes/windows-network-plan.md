# Proposed mixed native GPU / kind lab network

This is a plan awaiting the coordinator's endpoint confirmation. No host routes,
DNS configuration, WSL/dockerd/Caboose changes, Windows services or firewall rules
have been changed. The native specialist is not a Kubernetes GPU pod; record
mixed native GPU execution separately from Helm/device-plugin scheduling.

Confirmed WSL endpoints: mirrored Tailscale `100.115.49.93`, LAN `192.168.68.85`,
kind bridge `172.20.0.1`. The same Tailscale address reaches native Windows SSH
from the coordinator's Mac (`DESKTOP-CR5L3VD`) but WSL→that address:22 is refused
before authentication. Do not copy a Mac SSH private key into WSL.

The coordinator can execute inventory safely through the existing Mac SSH alias:

```powershell
powershell -NoProfile -File C:\ether-gpu-lab\windows-gpu-lab.ps1 -Action Inventory
```

Use only `C:\ether-gpu-lab`, exact reviewed `ether.exe` and checksum-verified pinned
Windows FFmpeg bundle. `-Action Encode` runs 48 actual native h264_nvenc frames
and writes only the generated encoder log. No provider/claim is involved. Do not
start the specialist until the following transport and advertised-origin checks
are satisfied. Its strict deployment parser must include ledgerCapacity support.

## Required endpoints

| Consumer | Destination | Proposed transport |
| --- | --- | --- |
| Windows specialist | lab PG | Windows loopback `127.0.0.1:15435` through an owned scoped relay/tunnel to lab PG5432 |
| Windows specialist | full/source private0 | `https://RELAY_TAILSCALE_IP:33431`, exact advertised origin for full0 |
| Windows specialist | full/source private1 | `https://RELAY_TAILSCALE_IP:33432`, exact advertised origin for full1 |
| kind full/source pods | Windows encoder | `https://RELAY_TAILSCALE_IP:33411`, relay to native private32411 |
| Windows local inspector | native probes | loopback32412; never public ingress |

`RELAY_TAILSCALE_IP` must be the coordinator's reachable Mac Tailscale IPv4 (not
the mirrored Windows/WSL address). An owned user-space TCP relay on that address
can connect to WSL's scoped port-forwards33431/33432 and a Mac SSH local forward
to Windows private32411. This avoids assuming WSL can reach native Windows's
mirrored self-address. Native Windows can reach the Mac's real Tailscale address.
The coordinator must supply the actual Mac address and confirm this path before
any listener is opened. Prefer a direct validated host-loopback path if available;
do not modify global routes/DNS just to make the plan work.

To make the PG path explicit, a Mac SSH remote forward can bind Windows
loopback15435 and connect from the Mac to WSL `100.115.49.93:15435`, where an owned
kubectl port-forward reaches the lab PG service. First independently verify the
Mac reaches that WSL listener and Windows reaches the loopback forward. No DB
credentials belong in forwarding command arguments. A separate lab URL file on
Windows must point at the verified loopback endpoint, with the generated lab
password transferred securely as a file; never print it or put it in Helm values.

## Private identity origins must match the path

Windows cannot resolve/reach Kubernetes `.svc.cluster.local` names by default.
Merely opening PG or encoder ports is insufficient. Before native enrollment,
configure each full pod's node-local deployment internalAddress to its exact
relay origin above, then drain/restart sequentially so membership, minted
certificate hostname and endpoint checks agree. Keep each full pod's existing
identity and PVC; no node.json copy. These are lab-specific per-ordinal address
overrides, not a reason to expose the private service publicly in the chart.
The private listener remains32401 inside each pod and uses the existing issuer.
All kind nodes must also reach the relay origin in the advertised membership.
Check TLS trust/client authentication and actual node/boot identity both ways.
A successful TCP connection alone does not establish private compatibility.

If any side cannot reach the exact advertised endpoint, stop and report the
nonsecret error. Do not bypass hostname, issuer, role or membership verification.
Use a reviewed dedicated source/native graph with generated input to test this.

## Native setup and cleanup

Only after those endpoints are proven, stage the lab DB URL/raw32-byte key/issuer
certificate+key/roots under the script's restricted `secrets` directory using a
secure file transfer. The script never logs their bytes and restricts that folder
to the current Windows user and SYSTEM. Use the same verified fleet ledger quota
and a known clock-error bound. `-Action Start` requires explicit peers/origin,
checks transport, runs real NVENC smoke, writes deployment JSON path refs only,
and starts one BelowNormal owned process with an exact PID file. It does not
create a service or firewall rule.

A firewall exception, if needed, must be separately explicit, named for this lab,
limited to private32411 and the exact relay source address, then removed after
acceptance. Do not expose probes or open broad application rules. Keep the
Mac relay/SSH process IDs as well as the Windows PID; stop only those exact owned
processes. Drain successfully before planned shutdown. For abrupt native encoder
loss, capture actual process termination and durable fencing/capacity evidence;
PID disappearance is relevant only to that exact owned encoder process tree,
not proof of unrelated source retirement. Preserve unresolved claims on failure.

The reviewed cleanup design uses `native-launcher/main_windows.go`, compiled to
`C:\ether-gpu-lab\ether-lab-launcher.exe`; no PowerShell upgrade is required. The
launcher creates Ether suspended, assigns its exact handle to a fresh unnamed
kill-on-close job with a noninheritable job handle and no breakaway flags, records
the child PID, then resumes it. Killing the exact owned launcher on bookkeeping
failure closes the job and terminates all job descendants. This must be tested
natively; compilation alone establishes no runtime safety claim.

For a credential-free native containment test, invoke the staged launcher with
`--self-test`. It starts only copies of itself: one assigned suspended child and
one descendant leaf. The files `specialist.child.pid` and `probe.leaf.pid` record
their exact PIDs. Inspect actual job membership and creation times, then kill the
exact captured launcher Process object; verify both recorded children terminate.
No Ether binary, listener or Secret is needed for that test. Also test forced
PID-record write failure: the launcher must kill its still-suspended child before
any code executes. A separate real FFmpeg descendant containment test remains
required when the native FFmpeg bundle is approved/staged. Do not start the
specialist on the strength of only the generic self-test.

## Native evidence recorded by the coordinator

On 2026-11-05 the coordinator verified the pinned Windows archive SHA256
`3691f8b2d4c39c511b5faff1c8c4687bd782dac823e63ba69f0755f7ce1af9ce` and ran
48 actual `h264_nvenc` frames on the RTX4080. A retained .NET Process handle
reported exit0 and zero stderr bytes (PID14776, exited). An earlier
Start-Process wrapper lost its ExitCode; that harness result was discarded.

The coordinator also verified launcher binary SHA256
`94abd125a635337b72dce060af032e3ea20ae841767edc79ee8b0130349a1bd1` and captured
exact self-test ancestry launcher3644 → assigned child2448 → leaf6396. Killing
the exact launcher made both retained child/leaf Process handles exit; handles
were disposed and owned PID files removed. No native process remained. These
are native encoder and generic job-containment proofs, not distributed GPU or
node-loss acceptance.

The added fixed `--self-test-ffmpeg` mode creates an assigned suspended branch
copy of the launcher, which starts only
`C:\ether-gpu-lab\ffmpeg-8.1.3-1\ffmpeg.exe`. Its arguments are fixed: realtime
640x360/24fps lavfi test video, 30 seconds/720 frames maximum, `h264_nvenc`, null
output, and progress on stdout. A separate 40-second deadline and three-second
pipe wait bound failure cleanup. No arbitrary command, provider, file input,
listener or Secret is used. `specialist.child.pid` identifies the branch;
`probe.ffmpeg.pid` identifies the actual encoder descendant. Use a fresh copied
binary only after verifying the new supplied SHA256.

For the FFmpeg containment test, retain .NET Process objects **and open their
Handle properties immediately** for launcher, branch and FFmpeg. Verify exact
creation times, parent relationships and fixed FFmpeg executable path, then wait
for nonzero frame progress before killing the exact launcher Process object.
Confirm both captured descendant handles exit within a bounded interval and
dispose all handles. Do not trust an absent ExitCode from a detached
Start-Process wrapper, PID-only lookup after reuse, encoder listings, or a
compile-only result. Do not start the specialist before this test is reviewed
and passes.
