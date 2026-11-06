# Proposed mixed native GPU / kind lab network

## Current native staging constraints

The native lab can also reside at `D:\ether-gpu-lab`. The launcher and startup
script allow only the two explicit C/D lab roots; pass `-LabRoot D:\ether-gpu-lab`
to the startup script and `-Lab D:\ether-gpu-lab` to the containment test. Native
probes bind loopback only. Inventory returns before directory or ACL changes.
Do not write to C while it has no free space. The WSL disk is already on D;
relocating the lab does not require changing or compacting that disk.

Cross-compile Ether and the launcher on bb-worker-server with `CGO_ENABLED=0`,
`GOOS=windows`, and `GOARCH=amd64`; do not build Go on the GPU workstation.
Download into the selected lab root and verify the supplied SHA256 before
renaming or execution. Verify the Windows FFmpeg archive against
`packaging/ffmpeg.lock.json` and preserve the verified executable hashes when
copying the extracted bundle to D. Keep only native GPU execution here.

From WSL, record `cat /proc/pressure/memory` immediately before every native
encode or specialist start. If `full avg10` exceeds 10, wait and recheck; report
persistent pressure. WSL interop can run native PowerShell directly without a
restart. Keep native test handles until bounded cleanup proves every owned
descendant exited. These preparation checks do not prove cluster playback.

## Earlier endpoint proposal

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

## Dedicated-role endpoint proposal after CPU media passes

This graph supersedes the earlier full-only origin proposal. It is documentation
only: no new source relay, native process, Secret transfer or advertised-origin
change has been made. The coordinator's confirmed Mac Tailscale IPv4 is
`100.65.221.123`. Existing scoped old-full forwards `32431/32432` and PG `15435`
on WSL `100.115.49.93` remain retained and unchanged.

| Consumer | Destination | Proposed exact path |
| --- | --- | --- |
| Mac relay | dedicated source | new owned WSL `100.115.49.93:32433` → exact source pod `:32401` |
| Native worker and kind server | source private origin | `https://100.65.221.123:33433` → Mac TCP relay → WSL `32433` |
| Kind server/delivery/source | native worker private origin | `https://100.65.221.123:33411` → owned Mac SSH local forward → Windows loopback `32411` |
| Native worker | lab PostgreSQL | Windows loopback `127.0.0.1:15435` → owned SSH remote forward → WSL scoped `15435` → lab PG `5432` |
| Windows local inspector | native worker probes | loopback `32412`; no public listener |

The current source is `ether035-roles-ether-source-0`, UID
`11ff0ce9-6155-4c6f-8674-6285cbcbcdfa`, stable node
`2fafed8783aff03b06528063e2474b36`, boot
`f9a5fb8349bc25bb46ec216d86b549d8`. These are observations from candidate982;
recheck exact UID and boot after any approved probe-fix rollout before binding
an exact-pod forward. Record every owned forward/relay process and its endpoint.
Do not use the similarly named synthetic `ether035-ha-source` HTTP/NNTP fixture
as the private source runtime endpoint.

The source currently advertises its authenticated pod-DNS origin
`https://ether035-roles-ether-source-0.ether035-roles-ether-private.ether035-ha.svc.cluster.local:32401`.
Its existing certificate needs that DNS SAN for current validation. Before a
future reviewed source-origin change to Mac `33433`, require actual reachability
from both native Windows and every relevant kind pod, then remint through the
normal issuer with **IP SAN `100.65.221.123`**. The native worker's distinct
`33411` origin likewise requires that IP SAN. Their URI identities, boot checks,
role checks, issuer trust and mTLS remain separate; a shared relay IP is not node
identity. A DNS SAN containing an IP string is not an IP SAN.

Server and delivery private origins can remain pod DNS: the native encoder reads
from the dedicated source and PostgreSQL, while kind server/delivery call its
advertised relay origin. Validate this dependency graph against the final
candidate's actual private calls before native startup; any additional peer must
have an explicit verified endpoint. Do not change old full-node advertisements.
The chart currently generates pod-DNS addresses. A future one-source LAB override
must be a separately reviewed exact ConfigMap/render change followed by successful
source drain and UID-fenced replacement, preserving its PVC/node ID. No such live
change is authorized by this plan or required for CPU HLS acceptance.

For the PG path, an owned Mac SSH remote forward binds Windows loopback15435 and
connects from Mac to WSL `100.115.49.93:15435`. Verify both connections before
using a generated lab DB URL file. No DB credential belongs in command arguments.
TCP success alone is insufficient: validate certificate chain, actual IP/DNS
hostname, missing-client-cert rejection and actual role/boot identity on every
private endpoint. No trust bypass or global DNS/route changes are permitted.

## Opaque lab Secret transfer proposal

After the corrected CPU media gate and endpoint/clock review pass, the coordinator
can transfer only the existing isolated lab Secret files into restricted native
`C:\ether-gpu-lab\secrets`: DB URL, raw32-byte cluster encryption key, issuer PEM
certificate, PKCS8 Ed25519 issuer private key and explicit roots PEM. Keep the
three logical bundles separate and preserve raw key bytes. Transfer via the
existing authenticated BB file mechanism into restricted coordinator task storage,
then existing Mac SSH/SFTP into that owned Windows directory; never place payloads
in a BB message, shell arguments, Git, Helm values or command output. Remove only
exact owned transfer copies after verified delivery. The public CA alone may be
shared for review. Deployment JSON contains paths only. Create a fresh native
node identity locally; never copy any pod's `node.json`.

This is an opaque-file workflow proposal, not an initiated transfer. Final exact
file roots, restricted ACL verification and source/worker origins must be reviewed
before secrets or a native specialist process are staged. No services, broad
firewall rules, host driver changes, daemon restarts or new software installation
are part of it.

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


The coordinator subsequently verified the actual NVENC-descendant containment
mode using launcher SHA256
`265f68851ebcdfbe314da269e95b8a4c861f61fdd8e9257958f565d038ec6883`.
The native PowerShell 5 test observed nonzero encoded frame progress and retained
the exact ancestry launcher9288 → assigned branch14388 → pinned FFmpeg5664.
Creation times were `2026-11-05T18:18:46.2805081Z`,
`2026-11-05T18:18:46.2976937Z`, and `2026-11-05T18:18:46.3107715Z`, respectively.
Killing the retained launcher terminated both retained descendants; all three
Process objects reported HasExited true and the stderr count was zero. The
script disposed the handles and removed only matching owned PID files. No native
process or service remained. This completes the native FFmpeg job-containment
check; it does not prove distributed GPU playback, fencing or node-loss recovery
(`distributedGPUVerified=false`). No additional containment build is needed.
