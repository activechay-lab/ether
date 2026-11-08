import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, it, expect, vi, afterEach } from "vitest";
import { webcrypto } from "node:crypto";
import { resetRegistryCache, type Descriptor } from "../lib/extend";
import { descriptors as notifyDescriptors } from "../mock/extend/descriptors/notify";
import { BrowserPush } from "../pages/notifications/BrowserPush";
import { ChannelDrawer } from "../pages/notifications/ChannelDrawer";
import { Channels } from "../pages/notifications/Channels";
import { ServerProtection } from "../pages/settings/ServerProtection";
import {
  blankChannel,
  channelKindDescriptors,
  eventKindDescriptors,
  type ChannelKindMeta,
  type NotificationsData,
} from "../pages/notifications/types";

const empty: NotificationsData = {
  channels: [],
  deliveries: [],
};

afterEach(() => {
  resetRegistryCache();
  vi.unstubAllGlobals();
});

function stubFetch(
  handler: (url: string, init?: RequestInit) => Response | Promise<Response> = () =>
    new Response("{}", { status: 200 }),
) {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.includes("/extend/registry")) {
      return new Response(JSON.stringify({ version: 1, descriptors: notifyDescriptors }), {
        status: 200,
      });
    }
    return handler(url, init);
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

function pushSupport(permission = "granted") {
  const requestPermission = vi.fn().mockResolvedValue(permission);
  vi.stubGlobal("isSecureContext", true);
  vi.stubGlobal("Notification", { permission: "default", requestPermission });
  vi.stubGlobal("PushManager", class {});
  vi.stubGlobal("crypto", webcrypto);
  const subscription = {
    endpoint: "https://push.example.test/device",
    toJSON: () => ({
      endpoint: "https://push.example.test/device",
      keys: { auth: "auth", p256dh: "key" },
    }),
    unsubscribe: vi.fn().mockResolvedValue(true),
  };
  const subscribe = vi.fn().mockResolvedValue(subscription);
  vi.stubGlobal("navigator", {
    platform: "Test phone",
    serviceWorker: {
      register: vi.fn().mockResolvedValue({}),
      ready: Promise.resolve({
        pushManager: { getSubscription: vi.fn().mockResolvedValue(null), subscribe },
      }),
    },
  });
  return { requestPermission, subscription, subscribe };
}

describe("Notification setup", () => {
  it("opens, saves and edits a channel on an HTTP LAN page without randomUUID", async () => {
    // randomUUID is secure-context-only; getRandomValues is available on HTTP.
    vi.stubGlobal("isSecureContext", false);
    vi.stubGlobal("crypto", { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) });
    const fetcher = stubFetch();
    const onChanged = vi.fn().mockResolvedValue(undefined);
    const { rerender } = render(<Channels data={empty} onChanged={onChanged} />);
    await waitFor(() =>
      expect(fetcher.mock.calls.some(([u]) => String(u).includes("/extend/registry"))).toBe(true),
    );
    fireEvent.click(screen.getByRole("button", { name: "New channel" }));
    const drawer = await screen.findByRole("dialog", { name: "New notification channel" });
    fireEvent.change(within(drawer).getByLabelText("Name"), { target: { value: "Server alerts" } });
    fireEvent.change(await within(drawer).findByLabelText("Endpoint URL"), {
      target: { value: "https://notify.example.test/hook" },
    });
    fireEvent.click(within(drawer).getByRole("button", { name: "Save channel" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
    const puts = fetcher.mock.calls.filter(
      ([u, init]) => String(u).includes("/notifications/") && init?.method === "PUT",
    );
    expect(puts).toHaveLength(1);
    const [url, request] = puts[0]!;
    const saved = JSON.parse(String(request?.body));
    expect(saved.id).toEqual(expect.any(String));
    expect(saved.id.length).toBeGreaterThan(0);
    expect(url).toContain(`/notifications/${saved.id}`);
    expect(request?.method).toBe("PUT");
    expect(saved).toMatchObject({
      name: "Server alerts",
      kind: "webhook",
      secrets: { endpoint: "https://notify.example.test/hook" },
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    rerender(<Channels data={{ ...empty, channels: [saved] }} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    const editor = await screen.findByRole("dialog", { name: "Edit notification channel" });
    expect(within(editor).getByLabelText("Name")).toHaveValue("Server alerts");
    fireEvent.click(within(editor).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(
      fetcher.mock.calls.filter(
        ([u, init]) => String(u).includes("/notifications/") && init?.method === "PUT",
      ),
    ).toHaveLength(1);
  });

  it("saves all email fields, recipient lists and explicit TLS mode", () => {
    const onSave = vi.fn();
    render(
      <ChannelDrawer
        open
        channel={{ ...blankChannel(notifyDescriptors, "smtp"), kind: "smtp" }}
        isNew
        descriptors={notifyDescriptors}
        saving={false}
        error={null}
        onClose={() => {}}
        onSave={onSave}
      />,
    );
    const drawer = screen.getByRole("dialog");
    fireEvent.change(within(drawer).getByLabelText("SMTP host:port"), {
      target: { value: "smtp.example.test:587" },
    });
    fireEvent.change(within(drawer).getByLabelText("From address"), {
      target: { value: "owner@example.test" },
    });
    fireEvent.change(within(drawer).getByLabelText("To addresses"), {
      target: { value: "one@example.test, two@example.test" },
    });
    fireEvent.click(within(drawer).getByLabelText("Weekly newsletter"));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save channel" }));
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: "smtp",
        settings: {
          smtpMode: "starttls",
          from: "owner@example.test",
          to: "one@example.test, two@example.test",
        },
        secrets: {
          endpoint: "smtp.example.test:587",
        },
        events: expect.arrayContaining(["server.crash", "newsletter.weekly"]),
      }),
    );
  });

  it("renders built-in and plugin channel kinds and schemas from the registry", async () => {
    const channels = channelKindDescriptors(notifyDescriptors);
    const events = eventKindDescriptors(notifyDescriptors);
    expect(channels.map((d) => d.id)).toEqual([
      "webpush",
      "webhook",
      "discord",
      "slack",
      "telegram",
      "smtp",
      "ntfy",
      "gotify",
    ]);
    expect(events).toHaveLength(22);

    const pluginDescriptor: Descriptor<ChannelKindMeta> = {
      kind: "channelKind",
      id: "plugin:community.matrix.notify/room",
      owner: "community.matrix.notify",
      label: { resolved: "Matrix" },
      hint: { resolved: "Post alerts to an encrypted Matrix room." },
      meta: {
        selectable: true,
        sendTest: true,
        fields: [
          {
            key: "homeserver",
            kind: "url",
            label: { resolved: "Homeserver URL" },
            required: true,
            wide: true,
          },
          {
            key: "room",
            kind: "text",
            label: { resolved: "Room ID" },
            required: true,
          },
          {
            key: "accessToken",
            kind: "secret",
            label: { resolved: "Room token" },
            required: true,
            secret: true,
          },
        ],
      },
    };
    const combined = [...notifyDescriptors, pluginDescriptor];
    const onSave = vi.fn();
    render(
      <ChannelDrawer
        open
        channel={blankChannel(combined)}
        isNew
        descriptors={combined}
        saving={false}
        error={null}
        onClose={() => {}}
        onSave={onSave}
      />,
    );
    const drawer = screen.getByRole("dialog");
    fireEvent.click(within(drawer).getByRole("button", { name: "Transport" }));
    fireEvent.click(
      await screen.findByRole("option", { name: "Matrix (from community.matrix.notify)" }),
    );
    expect(
      within(drawer).getByText("Post alerts to an encrypted Matrix room."),
    ).toBeInTheDocument();
    fireEvent.change(within(drawer).getByLabelText("Homeserver URL"), {
      target: { value: "https://matrix.example.org" },
    });
    fireEvent.change(within(drawer).getByLabelText("Room ID"), {
      target: { value: "!ops:example.org" },
    });
    fireEvent.change(within(drawer).getByLabelText("Room token"), {
      target: { value: "syt_secret_token" },
    });
    fireEvent.click(within(drawer).getByRole("button", { name: "Save channel" }));
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: "plugin:community.matrix.notify/room",
        settings: {
          homeserver: "https://matrix.example.org",
          room: "!ops:example.org",
        },
        secrets: {
          accessToken: "syt_secret_token",
        },
      }),
    );
  });

  it("tests a paused channel directly and reloads failed delivery history", async () => {
    const onChanged = vi.fn().mockResolvedValue(undefined);
    const fetcher = stubFetch(
      () =>
        new Response(JSON.stringify({ error: { message: "SMTP authentication failed" } }), {
          status: 502,
        }),
    );
    render(
      <Channels
        data={{
          ...empty,
          channels: [
            {
              ...blankChannel(notifyDescriptors, "smtp"),
              id: "mail",
              kind: "smtp",
              enabled: false,
            },
          ],
        }}
        onChanged={onChanged}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Send test" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(fetcher).toHaveBeenCalledWith(
      expect.stringContaining("/notifications/mail/test"),
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("does not subscribe or save when browser permission is declined", async () => {
    const { subscribe } = pushSupport("denied");
    const fetcher = stubFetch();
    render(<BrowserPush data={empty} descriptors={notifyDescriptors} onChanged={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Connect this device" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Allow notifications");
    expect(subscribe).not.toHaveBeenCalled();
    expect(fetcher.mock.calls.filter(([u]) => String(u).includes("/notifications/"))).toHaveLength(
      0,
    );
  });

  it("registers a device and saves its encrypted push subscription", async () => {
    const { subscribe } = pushSupport();
    const onChanged = vi.fn().mockResolvedValue(undefined);
    const fetcher = stubFetch(
      async (url: string) =>
        new Response(
          JSON.stringify(url.endsWith("/push/key") ? { publicKey: btoa("test-key") } : {}),
          { status: 200 },
        ),
    );
    render(<BrowserPush data={empty} descriptors={notifyDescriptors} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole("button", { name: "Connect this device" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(subscribe).toHaveBeenCalledWith(expect.objectContaining({ userVisibleOnly: true }));
    const put = fetcher.mock.calls.find(
      (c) => (c as unknown as [string, RequestInit])[1]?.method === "PUT",
    ) as unknown as [string, RequestInit];
    const body = JSON.parse(String(put[1].body));
    expect(body).toMatchObject({
      kind: "webpush",
      events: expect.arrayContaining(["server.crash"]),
    });
    expect(JSON.parse(body.secrets.subscription)).toMatchObject({
      endpoint: "https://push.example.test/device",
    });
  });

  it("removes a newly created subscription if saving the destination fails", async () => {
    const { subscription } = pushSupport();
    stubFetch(
      async (url: string) =>
        new Response(
          JSON.stringify(
            url.endsWith("/push/key")
              ? { publicKey: btoa("key") }
              : { error: { message: "Save failed" } },
          ),
          { status: url.endsWith("/push/key") ? 200 : 500 },
        ),
    );
    render(<BrowserPush data={empty} descriptors={notifyDescriptors} onChanged={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Connect this device" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Save failed");
    expect(subscription.unsubscribe).toHaveBeenCalled();
  });

  it("saves external outage monitoring without exposing its existing URL", async () => {
    const fetcher = stubFetch(
      async (url: string, init?: RequestInit) =>
        new Response(
          JSON.stringify(
            init?.method
              ? { ok: true }
              : String(url).endsWith("/notifications/")
                ? { crashWatcher: true }
                : { enabled: false, endpoint: "[configured]" },
          ),
          { status: 200 },
        ),
    );
    render(
      <MemoryRouter>
        <ServerProtection />
      </MemoryRouter>,
    );
    fireEvent.click(await screen.findByRole("switch", { name: "Send uptime heartbeat" }));
    fireEvent.click(screen.getByRole("button", { name: "Save heartbeat" }));
    await waitFor(() =>
      expect(
        fetcher.mock.calls.some(
          (c) =>
            c[1]?.method === "PUT" && JSON.parse(String(c[1].body)).endpoint === "[configured]",
        ),
      ).toBe(true),
    );
    expect(await screen.findByText("Watcher running on this node")).toBeInTheDocument();
  });
});
