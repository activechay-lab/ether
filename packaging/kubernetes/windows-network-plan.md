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
