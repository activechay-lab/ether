import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { destinationsFrom } from "../lib/sitemap";
import { descriptors as consoleDescriptors } from "../mock/extend/descriptors/console";
import type { PageMeta } from "../lib/console-descriptors";
import type { Descriptor } from "../lib/extend";

const destinations = () =>
  destinationsFrom(consoleDescriptors.filter((d) => d.kind === "page") as Descriptor<PageMeta>[]);
import type { ClusterStatus } from "../lib/types";
import {
  CLUSTER_POLL_MS,
  clusterWarnings,
  tokenState,
  transcodeLabel,
  managerURL,
  nodeRuntime,
  runtimeFresh,
  controlFresh,
  clusterNodeHealth,
} from "../pages/settings/Cluster";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };

function rowOf(region: HTMLElement, text: string): HTMLElement {
  const row = within(region).getByText(text).closest("tr");
  if (!row) throw new Error(`no row for ${text}`);
  return row as HTMLElement;
}

describe("Server › Cluster", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("is a sidebar page that search can find", async () => {
    expect(destinations().find((d) => d.href === "/cluster")).toMatchObject({ title: "Cluster" });
    renderApp("/cluster");
    expect(await screen.findByRole("heading", { name: "Cluster" }, WAIT)).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "Cluster" }).length).toBeGreaterThan(0);
  });

  it("offers guided create and join on a standalone server", async () => {
    const { fake } = renderApp("/cluster");
    expect(await screen.findByText("Standalone (SQLite)", {}, WAIT)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Create cluster" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Join cluster" })).toBeEnabled();
    expect(screen.queryByText(/ether cluster init/)).toBeNull();
    expect(screen.getByRole("link", { name: "Read the cluster guide" })).toHaveAttribute(
      "href",
      expect.stringContaining("docs/install.md#running-a-cluster"),
    );
    expect(screen.queryByRole("button", { name: /Create join token/ })).toBeNull();
    expect(screen.queryByRole("region", { name: "Nodes" })).toBeNull();
    expect(calls(fake, "GET", "/cluster/settings")).toHaveLength(0);
  });

  it("tests a database, reviews the plan and automatically creates the cluster", async () => {
    const { fake } = renderApp("/cluster");
    fireEvent.click(await screen.findByRole("button", { name: "Create cluster" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "Create cluster" });
    const next = within(drawer).getByRole("button", { name: "Test connection and continue" });
    expect(next).toBeDisabled();
    fireEvent.change(within(drawer).getByLabelText("Database host"), {
      target: { value: "db.lan" },
    });
    fireEvent.change(within(drawer).getByLabelText("Database password"), {
      target: { value: "secret@:/" },
    });
    fireEvent.click(next);
    await within(drawer).findByText("Connection verified", {}, WAIT);
    expect(calls(fake, "POST", "/cluster/setup/check")[0]!.body).toMatchObject({
      mode: "create",
      databaseUrl: "postgresql://ether:secret%40%3A%2F@db.lan:5432/ether?sslmode=verify-full",
    });
    expect(calls(fake, "POST", "/cluster/setup/apply")).toHaveLength(0);
    expect(within(drawer).getByText(/Create a portable backup/)).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: "Create cluster and restart" }));
    await screen.findByRole("tab", { name: "Nodes" }, WAIT);
    expect(calls(fake, "POST", "/cluster/setup/apply")).toHaveLength(1);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("warns when the owner explicitly weakens database transport protection", async () => {
    renderApp("/cluster");
    fireEvent.click(await screen.findByRole("button", { name: "Create cluster" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "Create cluster" });
    const tls = within(drawer).getByLabelText("Database encryption");
    expect(tls).toHaveValue("verify-full");
    fireEvent.change(tls, { target: { value: "require" } });
    expect(within(drawer).getByRole("alert")).toHaveTextContent("does not verify");
    fireEvent.change(tls, { target: { value: "disable" } });
    expect(within(drawer).getByRole("alert")).toHaveTextContent("without encryption");
  });

  it("requires explicit replacement and allows recovery from an invalid join token", async () => {
    const { fake } = renderApp("/cluster");
    fireEvent.click(await screen.findByRole("button", { name: "Join cluster" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "Join cluster" });
    fireEvent.change(within(drawer).getByLabelText("Join token"), { target: { value: "wrong" } });
    fireEvent.click(within(drawer).getByRole("button", { name: "Test connection and continue" }));
    await within(drawer).findByText(/Confirm replacement/, {}, WAIT);
    fireEvent.click(within(drawer).getByRole("checkbox"));
    fireEvent.click(within(drawer).getByRole("button", { name: "Test connection and continue" }));
    await within(drawer).findByText("The join token is not valid.", {}, WAIT);
    expect(calls(fake, "POST", "/cluster/setup/apply")).toHaveLength(0);
    fireEvent.change(within(drawer).getByLabelText("Join token"), {
      target: { value: "ether-join-v1:example" },
    });
    fireEvent.click(within(drawer).getByRole("button", { name: "Test connection and continue" }));
    await within(drawer).findByText("Connection verified", {}, WAIT);
    expect(within(drawer).getByText(/existing history is not merged/)).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: "Join cluster and restart" }));
    await screen.findByRole("tab", { name: "Nodes" }, WAIT);
    expect(calls(fake, "POST", "/cluster/setup/apply")[0]!.body).toMatchObject({
      mode: "join",
      token: "ether-join-v1:example",
      replace: true,
    });
  });

  it("submits remote weight with boot/version and never calls pending applied", async () => {
    const { fake } = renderApp("/cluster", { cluster: true });
    fireEvent.click(await screen.findByRole("tab", { name: "Nodes" }, WAIT));
    fireEvent.click(await screen.findByRole("button", { name: "Manage this node" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "Manage" });
    await waitFor(() => expect(within(drawer).getByLabelText("Node weight")).toBeEnabled());
    fireEvent.change(within(drawer).getByLabelText("Node weight"), { target: { value: "3" } });
    fireEvent.click(within(drawer).getByRole("button", { name: "Save weight" }));
    await within(drawer).findByText("Pending");
    expect(calls(fake, "POST", "/cluster/nodes/n-den/control")[0]!.body).toMatchObject({
      boot: "boot-n-den",
      expectedVersion: 0,
      patch: { weight: 3 },
    });
    expect(within(drawer).getByRole("button", { name: "Save weight" })).toBeDisabled();
    expect(within(drawer).getByRole("button", { name: "Pause new streams" })).toBeDisabled();
  });

  it("lists every node with its role, health, load and capabilities", async () => {
    renderApp("/cluster", { cluster: true });
    fireEvent.click(await screen.findByRole("tab", { name: "Nodes" }, WAIT));
    const nodes = await screen.findByRole("region", { name: "Nodes" }, WAIT);
    const den = rowOf(nodes, "den");
    expect(within(den).getByText("Leader")).toBeInTheDocument();
    expect(within(den).getByText("This node")).toBeInTheDocument();
    expect(within(den).getByText("Healthy")).toBeInTheDocument();
    expect(within(den).getByText("videotoolbox")).toBeInTheDocument();
    expect(within(den).getByLabelText("den weight")).toHaveTextContent("1");

    const attic = rowOf(nodes, "attic");
    expect(within(attic).queryByText("Leader")).toBeNull();
    expect(within(attic).getByLabelText("attic streams")).toHaveTextContent("2");
    expect(within(attic).getByLabelText("attic weight")).toHaveTextContent("2");
    expect(within(attic).getByText("198.51.100.20")).toBeInTheDocument();
    expect(within(attic).getByRole("button", { name: "Manage" })).toBeEnabled();

    const garage = rowOf(nodes, "garage");
    expect(within(garage).getByText("Offline")).toBeInTheDocument();
    expect(within(garage).queryByText(/Last seen/)).toBeNull();
    expect(within(garage).getByText("Version 0.8.4")).toBeInTheDocument();
    expect(within(garage).getByText("Software")).toBeInTheDocument();

    // Different public addresses among live nodes are called out.
    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    expect(screen.getByText(/different public addresses/)).toBeInTheDocument();

    expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
      "Overview",
      "Nodes",
      "Recovery history",
      "Join tokens",
    ]);
    fireEvent.click(screen.getByRole("tab", { name: "Join tokens" }));
    expect(screen.queryByRole("heading", { name: "Shared settings" })).toBeNull();
    expect(screen.queryByText("Settings each node keeps")).toBeNull();
    expect(screen.getByRole("button", { name: "Create join token" })).toBeEnabled();
  });

  it("keeps runtime details in the drawer while showing admission status", async () => {
    renderApp("/cluster", { cluster: true }, (fake) => {
      const fetch = fake.fetch;
      fake.fetch = async (...args) => {
        const response = await fetch(...args);
        if (!String(args[0]).endsWith("/cluster/")) return response;
        const status = (await response.json()) as ClusterStatus;
        status.self.incarnation = "current";
        status.nodes![0]!.incarnation = "current";
        status.selfRuntime = {
          nodeId: status.self.id,
          boot: "current",
          runtimeSampledAt: new Date().toISOString(),
          protocol: 1,
          started: true,
          readyExisting: true,
          acceptingNew: false,
          draining: true,
        };
        return new Response(JSON.stringify(status));
      };
    });
    fireEvent.click(await screen.findByRole("tab", { name: "Nodes" }, WAIT));
    const nodes = await screen.findByRole("region", { name: "Nodes" }, WAIT);
    const local = rowOf(nodes, "den");
    expect(within(local).getByText("Not taking new streams")).toBeInTheDocument();
    expect(within(local).getByText("Draining")).toBeInTheDocument();
    expect(within(local).queryByText("Serving existing work")).toBeNull();
    const peer = rowOf(nodes, "source-02");
    expect(within(peer).getByText("Not reported")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Manage this node" }));
    const drawer = await screen.findByRole("dialog", { name: "Manage" });
    expect(within(drawer).getByText("Accepting new work").nextElementSibling).toHaveTextContent(
      "No",
    );
  });

  it("lists join tokens with their state", async () => {
    renderApp("/cluster", { cluster: true });
    fireEvent.click(await screen.findByRole("tab", { name: "Join tokens" }, WAIT));
    const tokens = await screen.findByRole("region", { name: "Join tokens" }, WAIT);
    await within(tokens).findByText("Join token 1", {}, WAIT);
    expect(within(tokens).queryByText("t7k2")).toBeNull();
    expect(within(tokens).queryByText("expired-example")).toBeNull();
    const open = rowOf(tokens, "Join token 1");
    expect(within(open).getByText("Unused")).toBeInTheDocument();
    expect(within(open).getByRole("button", { name: "Revoke token 1" })).toBeEnabled();
  });

  it("creates a join token in a drawer and shows it only once", async () => {
    const { fake } = renderApp("/cluster", { cluster: true });
    fireEvent.click(await screen.findByRole("tab", { name: "Join tokens" }, WAIT));
    const tokens = await screen.findByRole("region", { name: "Join tokens" }, WAIT);
    fireEvent.click(within(tokens).getByRole("button", { name: /Create join token/ }));
    let drawer = await screen.findByRole("dialog", { name: "Create join token" });
    expect(within(drawer).queryByLabelText("Join token")).toBeNull();
    fireEvent.click(within(drawer).getByRole("radio", { name: "15 minutes" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Create join token" }));

    const field = await within(drawer).findByLabelText("Join token", {}, WAIT);
    const token = (field as HTMLTextAreaElement).value;
    expect(token).toMatch(/^ether-join-v1:/);
    expect(within(drawer).getByText("Keep this token secret")).toBeInTheDocument();
    expect(within(drawer).getByText(/database password/)).toBeInTheDocument();
    expect(within(drawer).getByText(/shown only once/)).toBeInTheDocument();
    expect(within(drawer).getByText(/choose Cluster → Join cluster/)).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: /Copy token/ })).toBeInTheDocument();
    expect(within(drawer).queryByRole("button", { name: /Copy command/ })).toBeNull();
    expect(calls(fake, "POST", "/cluster/join-tokens")[0]!.body).toEqual({ ttlMinutes: 15 });

    // A second readable label appears; closing the drawer forgets the secret.
    expect(await within(tokens).findByText("Join token 2", {}, WAIT)).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.queryByText(token)).toBeNull();
    fireEvent.click(within(tokens).getByRole("button", { name: /Create join token/ }));
    drawer = await screen.findByRole("dialog", { name: "Create join token" });
    expect(within(drawer).queryByLabelText("Join token")).toBeNull();
  });

  it("revokes an unused token after confirmation", async () => {
    const { fake } = renderApp("/cluster", { cluster: true });
    fireEvent.click(await screen.findByRole("tab", { name: "Join tokens" }, WAIT));
    const tokens = await screen.findByRole("region", { name: "Join tokens" }, WAIT);
    fireEvent.click(await within(tokens).findByRole("button", { name: "Revoke token 1" }, WAIT));
    const confirm = await screen.findByRole("dialog", { name: "Revoke join token?" });
    fireEvent.click(within(confirm).getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(calls(fake, "DELETE", "/cluster/join-tokens/q9wd")).toHaveLength(1));
    await waitFor(() => expect(within(tokens).queryByText("Join token 1")).toBeNull(), WAIT);
    expect(within(tokens).queryByText("t7k2")).toBeNull();
  });

  it("refreshes membership every 10 seconds while open", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { fake } = renderApp("/cluster", { cluster: true });
    await screen.findByRole("tab", { name: "Nodes" }, WAIT);
    const before = calls(fake, "GET", "/cluster/").filter((c) => c.path === "/cluster/").length;
    await act(async () => {
      vi.advanceTimersByTime(CLUSTER_POLL_MS);
    });
    await waitFor(() =>
      expect(
        calls(fake, "GET", "/cluster/").filter((c) => c.path === "/cluster/").length,
      ).toBeGreaterThan(before),
    );
    expect(calls(fake, "GET", "/cluster/settings").length).toBeGreaterThan(1);
  });
});

describe("cluster helpers", () => {
  const now = new Date("2026-10-19T12:00:00Z");
  const base: ClusterStatus = {
    mode: "postgres",
    self: {
      id: "a",
      name: "a",
      version: "1",
      addrs: [],
      caps: { transcode: true },
      load: 0,
      startedAt: "",
      lastSeen: "",
      leader: true,
      live: true,
    },
    epoch: 1,
    nodes: [],
  };

  it("binds runtime only to the captured local boot and ages it independently of heartbeat", () => {
    const self = { ...base.self, incarnation: "boot-a" };
    const runtime = {
      nodeId: self.id,
      boot: "boot-a",
      runtimeSampledAt: now.toISOString(),
      protocol: 1,
      started: true,
      readyExisting: true,
      acceptingNew: false,
      draining: true,
    };
    const status = { ...base, self, selfRuntime: runtime };
    expect(nodeRuntime(status, self)).toBe(runtime);
    for (const node of [
      { ...self, id: "peer" },
      { ...self, incarnation: "old" },
      { ...self, incarnation: undefined },
    ])
      expect(nodeRuntime(status, node)).toBeUndefined();
    expect(nodeRuntime({ ...status, self: { ...self, incarnation: "new" } }, self)).toBeUndefined();
    expect(nodeRuntime({ ...status, selfRuntime: undefined }, self)).toBeUndefined();
    expect(
      nodeRuntime({ ...status, selfRuntime: { ...runtime, nodeId: "peer" } }, self),
    ).toBeUndefined();
    expect(runtimeFresh(runtime.runtimeSampledAt, now.getTime())).toBe(true);
    for (const at of [
      "",
      "invalid",
      "0001-01-01T00:00:00Z",
      new Date(now.getTime() + 1).toISOString(),
      new Date(now.getTime() - 20_001).toISOString(),
    ])
      expect(runtimeFresh(at, now.getTime())).toBe(false);
  });

  it("keeps synthetic peer health separate from live runtime and heartbeat", () => {
    const peer = { ...base.self, id: "peer", incarnation: "peer-boot", live: false };
    const health = {
      peer: {
        nodeId: "peer",
        boot: "peer-boot",
        sampledAt: new Date().toISOString(),
        level: "error" as const,
      },
    };
    expect(clusterNodeHealth(base, peer, false)).toBe("unknown");
    expect(clusterNodeHealth(base, peer, false, health)).toBe("error");
    expect(clusterNodeHealth(base, peer, true, health)).toBe("unknown");
    expect(clusterNodeHealth(base, { ...peer, incarnation: "replacement" }, false, health)).toBe(
      "unknown",
    );
    expect(
      clusterNodeHealth(base, peer, false, {
        peer: { ...health.peer, sampledAt: "2000-01-01T00:00:00Z" },
      }),
    ).toBe("unknown");
  });

  it("rejects mismatched or stale remote observations", () => {
    const node = { ...base.self, incarnation: "boot-a" };
    const c = {
      nodeId: node.id,
      boot: "boot-a",
      updatedAt: now.toISOString(),
      requestedVersion: 1,
      appliedVersion: 1,
      state: "applied" as const,
      observed: { weight: 2, draining: false },
      capabilities: [],
    };
    expect(controlFresh(c, node, now.getTime())).toBe(true);
    expect(controlFresh({ ...c, boot: "old" }, node, now.getTime())).toBe(false);
    expect(controlFresh({ ...c, nodeId: "peer" }, node, now.getTime())).toBe(false);
    expect(controlFresh(c, node, now.getTime() + 30001)).toBe(false);
  });

  it("warns when this node loses the database, the event channel or clock sync", () => {
    const healthy = {
      channel: "c",
      busConnected: true,
      lastHeartbeat: "2026-10-19T11:59:58Z",
      clockSkewNs: 0,
    };
    expect(clusterWarnings({ ...base, health: healthy }, now)).toEqual([]);
    expect(
      clusterWarnings(
        { ...base, health: { ...healthy, lastHeartbeat: "2026-10-19T11:55:00Z" } },
        now,
      )[0],
    ).toMatch(/hasn't reached the database since 5 minutes ago/);
    expect(
      clusterWarnings({ ...base, health: { ...healthy, busConnected: false } }, now)[0],
    ).toMatch(/event connection/);
    expect(clusterWarnings({ ...base, health: { ...healthy, clockSkewNs: -5e9 } }, now)[0]).toMatch(
      /5 seconds off/,
    );
    expect(
      clusterWarnings({ ...base, error: "connection refused", warnings: ["nodes differ"] }, now),
    ).toEqual([
      "Nodes differ",
      "Couldn't read the cluster's nodes from the database: connection refused",
    ]);
    expect(
      clusterWarnings(
        {
          ...base,
          warnings: [
            "nodes reach providers from different public addresses (198.51.100.2: b; 203.0.113.1: a); a debrid account can be limited when it is used from more than one address",
          ],
        },
        now,
      )[0],
    ).toBe(
      "These computers reach indexers and debrid from different public addresses (198.51.100.2: b; 203.0.113.1: a). One debrid account can be limited when it is used from more than one address.",
    );
  });

  it("builds safe links to peer managers", () => {
    expect(
      managerURL({ ...base.self, addrs: ["javascript:alert(1)", "http://node.lan:32400"] }),
    ).toBe("http://node.lan:32400/ether/cluster");
    expect(managerURL({ ...base.self, addrs: ["https://user:secret@node.lan"] })).toBeNull();
    expect(managerURL({ ...base.self, addrs: null })).toBeNull();
  });

  it("classifies tokens and transcoders", () => {
    const t = { id: "x", createdAt: "2026-10-19T11:00:00Z", expiresAt: "2026-10-19T12:30:00Z" };
    expect(tokenState(t, now)).toBe("unused");
    expect(tokenState({ ...t, expiresAt: "2026-10-19T11:30:00Z" }, now)).toBe("expired");
    expect(tokenState({ ...t, usedAt: "2026-10-19T11:10:00Z" }, now)).toBe("used");
    expect(transcodeLabel({ ...base.self, caps: { roles: ["source"], transcode: false } })).toBe(
      "—",
    );
    expect(transcodeLabel({ ...base.self, caps: { roles: ["transcode"], transcode: false } })).toBe(
      "Unavailable",
    );
    expect(transcodeLabel({ ...base.self, caps: { transcode: true, hwAccel: "none" } })).toBe(
      "Software",
    );
    expect(transcodeLabel({ ...base.self, caps: { transcode: true, hwAccel: "vaapi" } })).toBe(
      "vaapi",
    );
  });
});

describe("Cluster details elsewhere", () => {
  it("edits node weight on General only on a cluster node", async () => {
    const { fake } = renderApp("/server", { cluster: true });
    fireEvent.click(
      await screen.findByRole("button", { name: "More options: This computer" }, WAIT),
    );
    const weight = await screen.findByLabelText("Node weight", {}, WAIT);
    fireEvent.change(weight, { target: { value: "3" } });
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/server")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/server")[0]!.body).toMatchObject({ clusterWeight: 3 });
  });

  it("hides node weight on a standalone server", async () => {
    renderApp("/server");
    await screen.findByLabelText("Server name", {}, WAIT);
    expect(screen.queryByLabelText("Node weight")).toBeNull();
  });

  it("shows which node holds each backup and saves a shared folder", async () => {
    const { fake } = renderApp("/maintenance/backups", { cluster: true });
    const snaps = await screen.findByRole("region", { name: "Snapshots" }, WAIT);
    expect(await within(snaps).findByText("den (this node)", {}, WAIT)).toBeInTheDocument();
    expect(within(snaps).getByText("attic")).toBeInTheDocument();
    const remote = fake.state.backups.find((b) => b.remote)!;
    expect(within(snaps).getByRole("button", { name: `Download ${remote.name}` })).toBeDisabled();
    expect(
      within(snaps).getByText("Stored on attic: download it from that node's admin page"),
    ).toBeVisible();
    const dir = await screen.findByLabelText("Shared backup folder", {}, WAIT);
    fireEvent.change(dir, { target: { value: "/mnt/backups/ether" } });
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "POST", "/adoption/backups/settings")).toHaveLength(1));
    expect(calls(fake, "POST", "/adoption/backups/settings")[0]!.body).toEqual({
      intervalHours: 24,
      retain: 7,
      sharedDir: "/mnt/backups/ether",
    });
  });

  it("has no shared folder or location column on a standalone server", async () => {
    renderApp("/maintenance/backups");
    const snaps = await screen.findByRole("region", { name: "Snapshots" }, WAIT);
    await within(snaps).findByRole("button", { name: /^Download/ }, WAIT);
    expect(within(snaps).queryByText("Location")).toBeNull();
    expect(screen.queryByLabelText("Shared backup folder")).toBeNull();
  });

  it("shows this node's share of provider connections", async () => {
    renderApp("/sources/usenet", { cluster: true });
    expect(
      await screen.findByText(/6 of 7 connections busy · this node: 7 of 20 connections/, {}, WAIT),
    ).toBeInTheDocument();
  });

  it("shows this node's share of debrid API connections", async () => {
    renderApp("/sources/debrid", { cluster: true });
    const list = await screen.findByRole("region", { name: "Debrid providers" }, WAIT);
    expect(
      (await within(list).findAllByText(/this node: 2 of 6 API connections/, {}, WAIT)).length,
    ).toBeGreaterThan(0);
  });

  it("shows no node share on a standalone server", async () => {
    renderApp("/sources/usenet");
    expect(await screen.findByText(/6 of 20 connections busy/, {}, WAIT)).toBeInTheDocument();
    expect(screen.queryByText(/this node:/)).toBeNull();
  });
});
