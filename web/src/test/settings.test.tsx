import { systemMessageKey, type MessageCatalog } from "@braedonsaunders/appkit-i18n";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { loadLocale } from "../i18n";
import de from "../i18n/locales/de.json";
import { calls, renderApp } from "./harness";
import { renderAppWith } from "./routes";

const german = (source: string) => {
  const entry = (de as MessageCatalog)[systemMessageKey(source)];
  if (typeof entry !== "string") throw new Error(`no German copy for ${source}`);
  return entry;
};

const WAIT = { timeout: 4000 };

afterEach(async () => {
  localStorage.clear();
  await loadLocale("en");
});

describe("Settings", () => {
  it("fills a proxy suggestion and tests the connection inline", async () => {
    const { fake } = renderApp("/server");
    fireEvent.click(
      await screen.findByLabelText("Send indexers, debrid and Usenet through a proxy", {}, WAIT),
    );
    fireEvent.click(screen.getByRole("button", { name: "Common proxy setups" }));
    fireEvent.click(
      within(await screen.findByRole("listbox", { hidden: true })).getByText(
        "VPN container (Gluetun) on a shared Docker network",
      ),
    );
    expect(screen.getByLabelText("Proxy address")).toHaveValue("http://gluetun:8888");
    fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
    expect(await screen.findByText("Split tunnel working", {}, WAIT)).toBeVisible();
    expect(calls(fake, "POST", "/outbound/check")[0]?.body).toMatchObject({
      enabled: true,
      url: "http://gluetun:8888",
    });
  });
  it("shows enabled app addresses and saves a non-Plex port from General", async () => {
    const { fake } = renderApp("/server", {}, (f) => {
      f.state.config.frontends = {
        plex: { enabled: true },
        jellyfin: { enabled: true },
        emby: { enabled: false },
      };
    });
    const port = await screen.findByLabelText("Jellyfin port", {}, WAIT);
    await waitFor(() => {
      expect(port).toBeEnabled();
      expect(port).toHaveValue(8096);
    }, WAIT);
    expect(screen.queryByLabelText("Emby port")).toBeNull();
    expect(screen.getByText("http://192.168.1.20:8096")).toBeVisible();
    expect(screen.getByText("https://jellyfin.example.com")).toBeVisible();
    fireEvent.change(port, { target: { value: "8098" } });
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(
      () =>
        expect(calls(fake, "PUT", "/frontends/jellyfin/settings")[0]?.body).toMatchObject({
          port: 8098,
        }),
      WAIT,
    );
  });
  it("shows and filters client families reported by other protocols", async () => {
    renderApp("/playback/devices");
    expect(await screen.findByText("Stremio TV", {}, WAIT)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Protocol" }));
    fireEvent.click(
      within(await screen.findByRole("listbox", { hidden: true })).getByText("Stremio"),
    );
    expect(screen.getByText("Stremio TV")).toBeInTheDocument();
    expect(screen.queryByText("Jellyfin TV")).toBeNull();
  });
  it("saves automatic franchise visibility without changing other catalog settings", async () => {
    const { fake } = renderApp("/services/tmdb");
    const toggle = await screen.findByLabelText("Hide automatic franchise collections", {}, WAIT);
    expect(toggle).not.toBeChecked();
    const before = { ...fake.state.config.catalog };
    fireEvent.click(toggle);
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/catalog")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/catalog")[0]!.body).toEqual({
      ...before,
      hideFranchiseCollections: true,
    });
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "Unsaved changes" })).toBeNull(),
    );
    fireEvent.click(screen.getByLabelText("Hide automatic franchise collections"));
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/catalog")).toHaveLength(2), WAIT);
    expect(calls(fake, "PUT", "/config/catalog")[1]!.body).toEqual({
      ...before,
      hideFranchiseCollections: false,
    });
  });

  it("round-trips redacted secrets and saves only the changed service", async () => {
    const { fake } = renderApp("/services/opensubtitles", {}, (f) => {
      f.state.config.openSubtitles = {
        enabled: true,
        apiKey: "••••",
        username: "sam",
        password: "••••",
      };
    });
    expect(await screen.findByRole("heading", { name: "Integrations" }, WAIT)).toBeInTheDocument();
    fireEvent.change(await screen.findByLabelText("Username", {}, WAIT), {
      target: { value: "sam.rivera" },
    });
    fireEvent.click(
      screen.getByRole("switch", {
        name: "Download a subtitle automatically when playback starts",
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/opensubtitles")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/opensubtitles")[0]!.body).toEqual({
      enabled: true,
      apiKey: "••••",
      username: "sam.rivera",
      password: "••••",
      automaticPreferred: true,
    });
    expect(calls(fake, "PUT", "/config/trakt")).toHaveLength(0);
    expect(calls(fake, "PUT", "/config/llm")).toHaveLength(0);
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "Unsaved changes" })).toBeNull(),
    );
  });

  it("lists catalogue sources with TMDB first alongside other integrations", async () => {
    renderApp("/services");
    const required = await screen.findByRole("region", { name: "Catalogues" }, WAIT);
    expect(screen.queryByRole("region", { name: "Essentials" })).toBeNull();
    expect(within(required).getByRole("listitem", { name: "TMDB" })).toBeInTheDocument();
    // The label follows the setup status, which loads after the page.
    expect(await screen.findByRole("button", { name: "Manage TMDB" }, WAIT)).toBeEnabled();
    const artwork = screen.getByRole("region", { name: "Artwork and ratings" });
    const posters = await within(artwork).findByRole("listitem", { name: "PostersPlus" }, WAIT);
    const ratings = within(artwork).getByRole("listitem", { name: "OMDb and MDBList" });
    expect(posters.parentElement).toBe(ratings.parentElement);
    for (const name of ["Debrid", "Prowlarr", "Plex"])
      expect(screen.queryByRole("listitem", { name })).toBeNull();
    for (const name of [/Catalog settings/, /Sources › Debrid/, /Sources › Indexers/])
      expect(screen.queryByRole("link", { name })).toBeNull();
    for (const [name, href] of [
      [/Set up Trakt/, "/services/trakt"],
      [/Manage Language model/, "/services/llm"],
      [/Set up OpenSubtitles/, "/services/opensubtitles"],
    ] as const)
      expect(screen.getByRole("link", { name })).toHaveAttribute("href", href);
    // Ratings and TheIntroDB only hold keys here; they are turned on where they're used.
    for (const [card, href] of [
      ["OMDb and MDBList", "/services/ratings"],
      ["TheIntroDB", "/services/introdb"],
    ] as const) {
      const item = screen.getByRole("listitem", { name: card });
      expect(
        within(item).getByRole("link", { name: new RegExp(`^(Manage|Set up) ${card}$`) }),
      ).toHaveAttribute("href", href);
      expect(within(item).getByRole("switch")).toBeInTheDocument();
    }
    expect(within(posters).getByRole("switch")).toBeInTheDocument();
    expect(within(screen.getByRole("listitem", { name: "Language model" })).getByText("On"));
    expect(screen.queryByRole("button", { name: "Test Trakt" })).toBeNull();
    expect(screen.getByRole("listitem", { name: "TheIntroDB" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save Simkl settings" })).toBeNull();
    expect(screen.queryByRole("switch", { name: "Prowlarr enabled" })).toBeNull();
    expect(screen.queryByRole("switch", { name: "TMDB enabled" })).toBeNull();
  });

  it("keeps the TheIntroDB key, health and test on its integration tab", async () => {
    const { fake } = renderApp("/services/introdb", {}, (f) => {
      f.state.config.markers = { theIntroDb: true, apiKey: "" };
    });
    expect(await screen.findByLabelText("Optional TheIntroDB key", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Test TheIntroDB" })).toBeInTheDocument();
    expect(screen.getAllByText("Public lookup").length).toBeGreaterThan(0);
    fireEvent.change(screen.getByLabelText("Optional TheIntroDB key"), {
      target: { value: "intro-key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Test TheIntroDB" }));
    await waitFor(() => expect(calls(fake, "POST", "/test/introdb")).toHaveLength(1), WAIT);
    expect(calls(fake, "POST", "/test/introdb")[0]!.body).toEqual({ apiKey: "intro-key" });
  });

  it("saves and tests the TMDB key from Integrations", async () => {
    const { fake } = renderApp("/services/tmdb");
    fireEvent.change(await screen.findByLabelText("Your own TMDB key (optional)", {}, WAIT), {
      target: { value: "tmdb-key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Test TMDB key" }));
    await waitFor(() => expect(calls(fake, "POST", "/test/tmdb")).toHaveLength(1), WAIT);
    expect(calls(fake, "POST", "/test/tmdb")[0]!.body).toEqual({ key: "tmdb-key" });
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/catalog")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/catalog")[0]!.body).toMatchObject({ tmdbKey: "tmdb-key" });
  });

  it("keeps a Trakt draft when another integration is toggled", async () => {
    const { fake } = renderApp("/services/trakt", {}, (f) => {
      f.state.config.trakt = { enabled: true, clientId: "old", clientSecret: "••••" };
      f.state.config.openSubtitles = {
        enabled: false,
        apiKey: "",
        username: "",
        password: "",
      };
    });
    fireEvent.change(await screen.findByLabelText("Client ID", {}, WAIT), {
      target: { value: "my-client" },
    });
    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    fireEvent.click(await screen.findByRole("switch", { name: "OpenSubtitles enabled" }, WAIT));
    fireEvent.click(await screen.findByRole("button", { name: "Save and enable" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/opensubtitles")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/opensubtitles")[0]!.body).toMatchObject({ enabled: true });
    expect(calls(fake, "PUT", "/config/trakt")).toHaveLength(0);
    fireEvent.click(screen.getByRole("tab", { name: "Trakt" }));
    expect(await screen.findByLabelText("Client ID", {}, WAIT)).toHaveValue("my-client");
  });

  it("cancels setup without enabling the service or opening its tab", async () => {
    const { fake } = renderApp("/services");
    fireEvent.click(await screen.findByRole("switch", { name: "Trakt enabled" }, WAIT));
    const dialog = await screen.findByRole("dialog", { name: "Set up Trakt" }, WAIT);
    expect(within(dialog).getByLabelText("Client ID")).toHaveValue("");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);
    expect(calls(fake, "PUT", "/config/trakt")).toHaveLength(0);
    expect(screen.getByRole("switch", { name: "Trakt enabled" })).not.toBeChecked();
    expect(screen.queryByRole("tab", { name: "Trakt" })).toBeNull();
  });

  it("enables a service only after setup saves the required field", async () => {
    const { fake } = renderApp("/services");
    fireEvent.click(await screen.findByRole("switch", { name: "Trakt enabled" }, WAIT));
    const dialog = await screen.findByRole("dialog", { name: "Set up Trakt" }, WAIT);
    fireEvent.change(within(dialog).getByLabelText("Client ID"), {
      target: { value: "cid" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save and enable" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/trakt")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/trakt")[0]!.body).toMatchObject({
      enabled: true,
      clientId: "cid",
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);
    expect(screen.getByRole("switch", { name: "Trakt enabled" })).toBeChecked();
    expect(screen.getByRole("tab", { name: "Trakt" })).toBeInTheDocument();
  });

  it("keeps the switch off when setup save is rejected", async () => {
    const { fake } = renderApp("/services", {}, (f) => {
      const orig = f.fetch;
      f.fetch = async (input, init) => {
        const url = new URL(String(input), "http://localhost");
        const path = url.pathname.replace(/^\/ether\/api/, "");
        const method = (init?.method ?? "GET").toUpperCase();
        if (method === "PUT" && path === "/config/trakt") {
          const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
          f.state.calls.push({ method, path, body });
          return new Response(
            JSON.stringify({
              error: {
                code: "validation",
                message: "configuration is invalid",
                fields: [{ field: "trakt.clientId", message: "looks malformed" }],
              },
            }),
            { status: 400 },
          );
        }
        return orig(input, init);
      };
    });
    fireEvent.click(await screen.findByRole("switch", { name: "Trakt enabled" }, WAIT));
    const dialog = await screen.findByRole("dialog", { name: "Set up Trakt" }, WAIT);
    fireEvent.change(within(dialog).getByLabelText("Client ID"), {
      target: { value: "bad id" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save and enable" }));
    expect((await screen.findAllByText("looks malformed", {}, WAIT)).length).toBeGreaterThan(0);
    expect(screen.getByRole("dialog", { name: "Set up Trakt" })).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Trakt enabled", hidden: true })).not.toBeChecked();
    expect(screen.queryByRole("tab", { name: "Trakt" })).toBeNull();
    expect(calls(fake, "PUT", "/config/trakt")).toHaveLength(1);
  });

  it("sends a disabled integration link back to the overview", async () => {
    renderApp("/services/trakt");
    expect(await screen.findByRole("tab", { name: "Overview" }, WAIT)).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.queryByRole("tab", { name: "Trakt" })).toBeNull();
    expect(await screen.findByRole("dialog", { name: "Set up Trakt" }, WAIT)).toBeInTheDocument();
  });

  it("turns a service off without sending its stored secrets", async () => {
    const { fake } = renderApp("/services", {}, (f) => {
      f.state.config.trakt = { enabled: true, clientId: "cid", clientSecret: "••••" };
    });
    fireEvent.click(await screen.findByRole("switch", { name: "Trakt enabled" }, WAIT));
    await waitFor(() => expect(calls(fake, "PUT", "/config/trakt")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/trakt")[0]!.body).toEqual({ enabled: false });
  });

  it("gives each service its own tab with its connection test", async () => {
    renderApp("/services/trakt", {}, (f) => {
      f.state.config.llm.enabled = true;
      f.state.config.trakt = {
        enabled: true,
        clientId: "cid",
        clientSecret: "",
      };
    });
    expect(await screen.findByRole("button", { name: "Test Trakt" }, WAIT)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Test endpoint" })).toBeNull();
    fireEvent.click(await screen.findByRole("tab", { name: "Language model" }, WAIT));
    expect(await screen.findByRole("button", { name: "Test endpoint" }, WAIT)).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "Language features" }, WAIT)).toBeVisible();
  });

  it("picks a language-model provider and a model from its live list", async () => {
    const { fake } = renderApp("/services/llm");
    fireEvent.click(await screen.findByRole("button", { name: "Provider" }, WAIT));
    fireEvent.click(await screen.findByRole("option", { name: /Anthropic — Claude/ }, WAIT));
    // Switching provider clears the old provider's model.
    expect(await screen.findByRole("button", { name: "Model" }, WAIT)).toHaveTextContent(
      "Choose a model",
    );
    fireEvent.change(screen.getByLabelText("API key"), { target: { value: "sk-ant-test" } });
    await waitFor(
      () =>
        expect(calls(fake, "POST", "/llm/models").at(-1)?.body).toMatchObject({
          provider: "anthropic",
          apiKey: "sk-ant-test",
        }),
      WAIT,
    );
    fireEvent.click(screen.getByRole("button", { name: "Model" }));
    fireEvent.click(await screen.findByRole("option", { name: /claude-opus-5-5/ }, WAIT));
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/llm")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/llm")[0]!.body).toEqual({
      enabled: true,
      provider: "anthropic",
      baseUrl: "",
      apiKey: "sk-ant-test",
      model: "claude-opus-5-5",
    });
  });

  it("accepts a typed model ID and shows the provider's listing error", async () => {
    renderApp("/services/llm", {}, (f) => {
      f.state.config.llm = {
        enabled: true,
        provider: "openai",
        baseUrl: "",
        apiKey: "bad",
        model: "",
      };
    });
    fireEvent.click(await screen.findByRole("button", { name: "Model" }, WAIT));
    expect(
      await screen.findByText(/Couldn't load models: 401: invalid key/, {}, WAIT),
    ).toBeTruthy();
    fireEvent.change(screen.getByPlaceholderText("Search or type a model ID…"), {
      target: { value: "gpt-5-preview" },
    });
    fireEvent.click(await screen.findByRole("option", { name: /gpt-5-preview/ }, WAIT));
    expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent("gpt-5-preview");
  });

  it("keeps unsaved edits across tabs and saves them together", async () => {
    const { fake } = renderApp("/services/trakt", {}, (f) => {
      f.state.config.trakt = { ...f.state.config.trakt, enabled: true };
      f.state.config.openSubtitles = { ...f.state.config.openSubtitles, enabled: true };
    });
    fireEvent.change(await screen.findByLabelText("Client ID", {}, WAIT), {
      target: { value: "my-client" },
    });
    fireEvent.click(screen.getByRole("tab", { name: "OpenSubtitles" }));
    fireEvent.change(await screen.findByLabelText("Username", {}, WAIT), {
      target: { value: "sam" },
    });
    fireEvent.click(screen.getByRole("tab", { name: "Trakt" }));
    expect(await screen.findByLabelText("Client ID", {}, WAIT)).toHaveValue("my-client");
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/opensubtitles")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/trakt")[0]!.body).toMatchObject({ clientId: "my-client" });
  });

  it("jumps to the tab holding a field the server rejected", async () => {
    renderApp("/services/trakt", {}, (f) => {
      f.state.config.trakt = {
        enabled: true,
        clientId: "old",
        clientSecret: "",
      };
      const orig = f.fetch;
      f.fetch = async (input, init) => {
        const url = new URL(String(input), "http://localhost");
        const path = url.pathname.replace(/^\/ether\/api/, "");
        const method = (init?.method ?? "GET").toUpperCase();
        if (method === "PUT" && path === "/config/trakt") {
          return new Response(
            JSON.stringify({
              error: {
                code: "validation",
                message: "configuration is invalid",
                fields: [{ field: "trakt.clientId", message: "looks malformed" }],
              },
            }),
            { status: 400 },
          );
        }
        return orig(input, init);
      };
    });
    fireEvent.change(await screen.findByLabelText("Client ID", {}, WAIT), {
      target: { value: "bad id" },
    });
    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    await screen.findByRole("region", { name: "Watch tracking" }, WAIT);
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(
      () =>
        expect(screen.getByRole("tab", { name: "Trakt" })).toHaveAttribute("aria-selected", "true"),
      WAIT,
    );
    expect(await screen.findByLabelText("Client ID", {}, WAIT)).toHaveValue("bad id");
    expect(screen.getAllByText("looks malformed").length).toBeGreaterThan(0);
    expect(screen.getByRole("region", { name: "Unsaved changes" })).toBeInTheDocument();
  });

  it("shows server validation errors next to the field", async () => {
    const { fake } = renderApp("/server");
    const name = await screen.findByLabelText("Server name", {}, WAIT);
    expect(screen.queryByRole("button", { name: /^Edit / })).toBeNull();
    const original = fake.fetch;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).endsWith("/config/server") && init?.method === "PUT") {
          return new Response(
            JSON.stringify({
              error: {
                code: "validation",
                message: "configuration is invalid",
                fields: [{ field: "server.friendlyName", message: "is required" }],
              },
            }),
            { status: 400 },
          );
        }
        return original(input, init);
      }),
    );
    fireEvent.change(name, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    expect(
      (await screen.findAllByText("Please fix the highlighted fields", {}, WAIT)).length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText("is required").length).toBeGreaterThan(0);
  });

  it("keeps General one page with no tabs for simkl, availability or shared cache", async () => {
    renderApp("/server", {}, (f) => {
      f.state.config = {
        ...f.state.config,
        simkl: { enabled: true, clientId: "id", clientSecret: "••••" },
        sharedCache: { dir: "/nas", budgetBytes: 1 },
      } as never;
    });
    expect(await screen.findByLabelText("Server name", {}, WAIT)).toBeInTheDocument();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    for (const name of ["Server", "Simkl", "Availability", "Shared Cache", "Downloads"])
      expect(screen.queryByRole("tab", { name })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "How Ether reaches the internet" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Run setup wizard" })).toBeVisible();
  });

  it("keeps unknown config off General and tucks power options under More options", async () => {
    renderApp("/server", {}, (f) => {
      f.state.config = {
        ...f.state.config,
        experiments: { enabled: true, explore: 0.2 },
      } as never;
    });
    expect(await screen.findByLabelText("Server name", {}, WAIT)).toBeInTheDocument();
    expect(await screen.findByLabelText("Plex port", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByText("Living Room Ether is ready for viewers.")).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Experiments" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Listen address")).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Advanced" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "More options: How apps connect" }));
    expect(screen.getByLabelText("Listen address")).toBeInTheDocument();
    // Moved off General: logging to Maintenance › Logs, stream limits to Playback.
    expect(screen.queryByLabelText("Log level")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "More options: Who can watch at once" }),
    ).not.toBeInTheDocument();
  });

  it("does not keep a General poster alias", async () => {
    renderApp("/server/posters");
    expect((await screen.findAllByRole("link", { name: "Overview" }, WAIT)).length).toBeGreaterThan(
      0,
    );
    expect(screen.queryByRole("heading", { name: "General" })).not.toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Ether posters" })).not.toBeInTheDocument();
  });

  it("keeps a Plex remote edit while Downloads saves from its Plex sub-tab", async () => {
    const { fake } = renderApp("/apps/plex/remote");
    const remote = await screen.findByLabelText("Allow remote access", {}, WAIT);
    const before = (remote as HTMLInputElement).checked;
    fireEvent.click(remote);
    fireEvent.click(screen.getByRole("tab", { name: "Downloads" }));
    fireEvent.click(await screen.findByLabelText("Friends can download", {}, WAIT));
    expect(screen.queryByLabelText("Allow remote access")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/plex")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/plex")[0]!.body).toMatchObject({ remoteAccess: !before });
    const wholeSaves = () => calls(fake, "PUT", "/config").filter((c) => c.path === "/config");
    await waitFor(() => expect(wholeSaves()).toHaveLength(1), WAIT);
    const whole = wholeSaves()[0]!.body as {
      plex: { remoteAccess: boolean };
      downloads: { allowFriends: boolean };
    };
    expect(whole.downloads.allowFriends).toBe(true);
    // The whole-config save merges into the live config, so it can't undo the Plex save.
    expect(whole.plex.remoteAccess).toBe(!before);
  });

  it("saves trailers on Pre-rolls and up-next picks on Extras together", async () => {
    const { fake } = renderApp("/playback/prerolls");
    type Saved = { extras: { enabled: boolean }; personalize: { postPlay: boolean } };
    const live = () => fake.state.config as unknown as Saved;
    const trailers = Boolean(live().extras?.enabled);
    const upNext = Boolean(live().personalize?.postPlay);
    fireEvent.click(await screen.findByLabelText("Trailers and Cinema Trailers", {}, WAIT));
    fireEvent.click(screen.getByRole("tab", { name: "Extras" }));
    fireEvent.click(await screen.findByLabelText("Personal “up next” picks", {}, WAIT));
    expect(screen.getByRole("switch", { name: "Skip intro" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    const wholeSaves = () => calls(fake, "PUT", "/config").filter((c) => c.path === "/config");
    await waitFor(() => expect(wholeSaves()).toHaveLength(2), WAIT);
    // Each whole-config save merges into the live config, so neither undoes the other.
    await waitFor(() => expect(live().extras.enabled).toBe(!trailers), WAIT);
    expect(live().personalize.postPlay).toBe(!upNext);
  });

  it("keeps personal touches on Home › Tuning for everyone, not for one viewer", async () => {
    renderApp("/home");
    fireEvent.click(await screen.findByRole("tab", { name: "Tuning" }, WAIT));
    expect(
      await screen.findByLabelText("Match and finish time on details", {}, WAIT),
    ).toBeVisible();
    expect(screen.getByLabelText("Adapt Home to tonight's browsing")).toBeVisible();
    expect(screen.queryByLabelText("Finish time format")).toBeNull();
  });

  it("keeps Plex account and connection settings inside its enabled app tab", async () => {
    renderApp("/apps/plex");
    expect(await screen.findByRole("button", { name: "Sign out of Plex" }, WAIT)).toBeVisible();
    expect(screen.getAllByText("Claimed by Plex account Braedon").length).toBeGreaterThan(0);
    expect(screen.queryByRole("switch", { name: "Claimed" })).toBeNull();
    expect(screen.queryByLabelText("Owner token")).toBeNull();
    expect(screen.queryByLabelText("Allow remote access")).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "Remote access" }));
    expect(await screen.findByRole("heading", { name: "Connection checks" }, WAIT)).toBeVisible();
    expect(screen.getByLabelText("Allow remote access")).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Secure connections" })).toBeNull();
  });

  it("rejects non-URL junk in Custom server URLs with a translated message while accepting Tailscale CGNAT URLs", async () => {
    const { fake } = renderApp("/apps/overview");
    fireEvent.click(
      await screen.findByRole("button", { name: "More options: Away from home" }, WAIT),
    );
    const input = await screen.findByLabelText("Custom server URLs", {}, WAIT);
    fireEvent.change(input, {
      target: { value: "[http://10.0.0.43:32450](http://10.0.0.43:32450/)" },
    });
    expect(
      (
        await screen.findAllByText(
          "Enter full http:// or https:// server URLs, separated by commas.",
          {},
          WAIT,
        )
      ).length,
    ).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    expect(calls(fake, "PUT", "/config/plex")).toHaveLength(0);

    fireEvent.click(screen.getAllByRole("button", { name: "Display language" })[0]!);
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /Deutsch/ }, WAIT));
    expect(
      (
        await screen.findAllByText(
          german("Enter full http:// or https:// server URLs, separated by commas."),
          {},
          WAIT,
        )
      ).length,
    ).toBeGreaterThan(0);

    fireEvent.change(await screen.findByLabelText(german("Custom server URLs"), {}, WAIT), {
      target: { value: "http://100.100.1.2:32400, https://plex.example.com:8443" },
    });
    expect(
      screen.queryByText(
        german("Enter full http:// or https:// server URLs, separated by commas."),
      ),
    ).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: new RegExp(german("Save changes")) }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/plex")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/plex")[0]!.body).toMatchObject({
      customUrls: ["http://100.100.1.2:32400", "https://plex.example.com:8443"],
    });
    // Only the away-from-home keys, so a Plex tab edit is never undone.
    expect(calls(fake, "PUT", "/config/plex")[0]!.body).not.toHaveProperty("remoteAccess");
  });

  it("returns disabled Plex settings to the overview without changing the saved account", async () => {
    const { fake } = renderApp("/apps/plex", {}, (f) => {
      f.state.config.frontends = { plex: { enabled: false } };
    });
    const accountBefore = JSON.stringify(fake.state.config.plex);
    expect(await screen.findByRole("switch", { name: "Plex enabled" }, WAIT)).not.toBeChecked();
    expect(screen.queryByRole("tab", { name: "Plex" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "Connection checks" })).toBeNull();
    expect(calls(fake, "PUT", "/config")).toHaveLength(0);
    expect(JSON.stringify(fake.state.config.plex)).toBe(accountBefore);
  });

  it("keeps an unsaved app setting while switching tabs", async () => {
    renderApp("/apps/jellyfin", {}, (f) => {
      f.state.config.frontends = { jellyfin: { enabled: true }, silo: { enabled: true } };
    });
    const quick = await screen.findByRole("switch", { name: "Quick Connect" }, WAIT);
    const before = (quick as HTMLInputElement).checked;
    fireEvent.click(quick);
    fireEvent.click(screen.getByRole("tab", { name: "Silo" }));
    fireEvent.click(await screen.findByRole("tab", { name: "Jellyfin" }, WAIT));
    expect(
      ((await screen.findByRole("switch", { name: "Quick Connect" }, WAIT)) as HTMLInputElement)
        .checked,
    ).toBe(!before);
    expect(screen.queryByRole("spinbutton", { name: "Port" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Server name" })).not.toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Unsaved changes" })).toBeVisible();
  });

  it("lets the owner opt in to the Jellyfin public viewer picker", async () => {
    const { fake } = renderApp("/apps/jellyfin", {}, (f) => {
      f.state.config.frontends = { jellyfin: { enabled: true } };
    });
    fireEvent.click(
      await screen.findByRole("button", { name: "More options: Find this server" }, WAIT),
    );
    const picker = await screen.findByLabelText(
      "Show local viewer names on the sign-in screen",
      {},
      WAIT,
    );
    expect(picker).not.toBeChecked();
    fireEvent.click(picker);
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(
      () => expect(calls(fake, "PUT", "/frontends/jellyfin/settings")).toHaveLength(1),
      WAIT,
    );
    expect(calls(fake, "PUT", "/frontends/jellyfin/settings")[0]!.body).toMatchObject({
      publicViewerPicker: true,
    });
  });

  it("turns personalized Jellyfin home rows off without forgetting the count", async () => {
    const { fake } = renderApp("/apps/jellyfin", {}, (f) => {
      f.state.config.frontends = { jellyfin: { enabled: true } };
    });
    const rows = await screen.findByRole("switch", { name: "Personalized rows" }, WAIT);
    expect(rows).toBeChecked();
    fireEvent.click(rows);
    fireEvent.click(screen.getByRole("button", { name: "More options: Home" }));
    fireEvent.change(await screen.findByRole("spinbutton", { name: "How many rows" }), {
      target: { value: "2" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(
      () => expect(calls(fake, "PUT", "/frontends/jellyfin/settings")).toHaveLength(1),
      WAIT,
    );
    expect(calls(fake, "PUT", "/frontends/jellyfin/settings")[0]!.body).toMatchObject({
      homeRows: false,
      homeRowCount: 2,
    });
  });

  it("does not keep the old Connectivity URL", async () => {
    renderApp("/remote-access/secure");
    expect(await screen.findByRole("link", { name: "Home" }, WAIT)).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Apps" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Connectivity" })).not.toBeInTheDocument();
  });

  it("puts every server page in the main sidebar, with no separate settings menu", async () => {
    renderApp("/server");
    await screen.findByRole("heading", { name: "General" }, WAIT);
    const sidebar = (name: string) => screen.queryAllByRole("link", { name });
    for (const name of ["General", "Apps", "Integrations", "Notifications", "Maintenance"])
      expect(sidebar(name).length).toBeGreaterThan(0);
    for (const name of ["Backups & import", "Updates", "Troubleshooting", "Logs"])
      expect(sidebar(name)).toHaveLength(0);
    expect(sidebar("Settings")).toHaveLength(0);
    // Config keys owned by other pages never show up as extra sections here.
    for (const name of ["Usenet", "Indexers", "Cache", "Resolver", "Quality", "Catalog", "Markers"])
      expect(screen.queryByRole("heading", { name })).toBeNull();
  });

  it("persists off and keeps an unsaved Simkl client id", async () => {
    const { fake } = renderApp("/services/simkl", {}, (f) => {
      f.state.config.simkl = { enabled: true, clientId: "app-id", clientSecret: "" };
    });
    fireEvent.change(await screen.findByLabelText("Client ID", {}, WAIT), {
      target: { value: "draft-client" },
    });
    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    const simklSwitch = () => screen.getByRole("switch", { name: "Simkl enabled" });
    fireEvent.click(await screen.findByRole("switch", { name: "Simkl enabled" }, WAIT));
    await waitFor(() => expect(calls(fake, "PUT", "/config/simkl")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/simkl")[0]!.body).toEqual({ enabled: false });
    await waitFor(() => expect(simklSwitch()).not.toBeChecked(), WAIT);
    await waitFor(() => expect(simklSwitch()).toBeEnabled(), WAIT);
    expect(screen.queryByRole("tab", { name: "Simkl" })).toBeNull();
    fireEvent.click(simklSwitch());
    fireEvent.click(await screen.findByRole("button", { name: "Save and enable" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/simkl")).toHaveLength(2), WAIT);
    expect(calls(fake, "PUT", "/config/simkl")[1]!.body).toMatchObject({
      enabled: true,
      clientId: "draft-client",
    });
    await waitFor(() => expect(simklSwitch()).toBeChecked(), WAIT);
    expect(await screen.findByRole("tab", { name: "Simkl" }, WAIT)).toBeInTheDocument();
  });

  it("reflects Simkl enabled after setup and keeps the ratings key on its own tab", async () => {
    const { fake } = renderApp("/services");
    fireEvent.click(await screen.findByRole("switch", { name: "Simkl enabled" }, WAIT));
    const simkl = await screen.findByRole("dialog", { name: "Set up Simkl" }, WAIT);
    fireEvent.change(within(simkl).getByLabelText("Client ID"), {
      target: { value: "app-id" },
    });
    fireEvent.click(within(simkl).getByRole("button", { name: "Save and enable" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/simkl")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/simkl")[0]!.body).toMatchObject({
      enabled: true,
      clientId: "app-id",
    });
    expect(screen.getByRole("switch", { name: "Simkl enabled" })).toBeChecked();
    expect(await screen.findByRole("tab", { name: "Simkl" }, WAIT)).toBeInTheDocument();

    expect(screen.getByRole("switch", { name: "OMDb and MDBList enabled" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Ratings" }));
    fireEvent.change(await screen.findByLabelText("API key", {}, WAIT), {
      target: { value: "omdb-key" },
    });
    expect(screen.queryByRole("switch", { name: "External ratings" })).toBeNull();
    expect(screen.queryByLabelText("Plex rating")).toBeNull();
    expect(screen.getByRole("link", { name: "Choose which scores apps show" })).toHaveAttribute(
      "href",
      "/metadata/ratings",
    );
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(
      () => expect(calls(fake, "PUT", "/discovery/ratings/settings")).toHaveLength(1),
      WAIT,
    );
    expect(calls(fake, "PUT", "/discovery/ratings/settings")[0]!.body).toMatchObject({
      apiKey: "omdb-key",
    });
    expect(screen.getByText("Connected")).toBeInTheDocument();
  });

  it("shows ratings status on its card and keeps score choices off Integrations", async () => {
    const ratings = {
      enabled: true,
      provider: "omdb",
      apiKey: "secret",
      critic: "tomatoes",
      audience: "imdb",
      cacheHours: 168,
    };
    const { fake } = renderAppWith("/services/ratings", (_method, path) =>
      path === "/discovery/ratings/settings" ? ratings : undefined,
    );
    expect(await screen.findByLabelText("Provider", {}, WAIT)).toHaveValue("omdb");
    expect(screen.getByLabelText("API key")).toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "External ratings" })).toBeNull();
    expect(screen.queryByLabelText("Keep scores for")).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    const card = await screen.findByRole("listitem", { name: "OMDb and MDBList" }, WAIT);
    expect(within(card).getByText("Configured")).toBeInTheDocument();
    expect(within(card).getByRole("switch")).toBeInTheDocument();
    expect(calls(fake, "PUT", "/discovery/ratings/settings")).toHaveLength(0);
  });

  it("translates integration tabs when the locale changes and keeps the draft", async () => {
    renderApp("/services/trakt", {}, (f) => {
      f.state.config.trakt = { enabled: true, clientId: "old", clientSecret: "" };
    });
    fireEvent.change(await screen.findByLabelText("Client ID", {}, WAIT), {
      target: { value: "keep-me" },
    });
    fireEvent.click(screen.getAllByRole("button", { name: "Display language" })[0]!);
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /Deutsch/ }));
    expect(await screen.findByLabelText(german("Client ID"), {}, WAIT)).toHaveValue("keep-me");
    expect(screen.getByRole("tab", { name: german("Language model") })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: german("Overview") })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Language model" })).toBeNull();
  });

  it("adopts a redacted save response and still keeps an unrelated draft", async () => {
    let release: (body: unknown) => void = () => {};
    const { fake } = renderApp("/services/trakt", {}, (f) => {
      f.state.config.openSubtitles = {
        enabled: true,
        apiKey: "••••",
        username: "sam",
        password: "••••",
      };
      f.state.config.trakt = { enabled: true, clientId: "old", clientSecret: "••••" };
      const orig = f.fetch;
      f.fetch = async (input, init) => {
        const url = new URL(String(input), "http://localhost");
        const path = url.pathname.replace(/^\/ether\/api/, "");
        const method = (init?.method ?? "GET").toUpperCase();
        if (method === "PUT" && path === "/config/opensubtitles") {
          const sent = JSON.parse(String(init?.body)) as Record<string, unknown>;
          f.state.calls.push({ method, path, body: sent });
          const value = await new Promise<unknown>((resolve) => {
            release = resolve;
          });
          return new Response(JSON.stringify(value), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        }
        return orig(input, init);
      };
    });
    fireEvent.click(await screen.findByRole("tab", { name: "OpenSubtitles" }, WAIT));
    fireEvent.change(await screen.findByLabelText("Password", {}, WAIT), {
      target: { value: "new-secret" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/opensubtitles")).toHaveLength(1), WAIT);
    fireEvent.click(screen.getByRole("tab", { name: "Trakt" }));
    fireEvent.change(await screen.findByLabelText("Client ID", {}, WAIT), {
      target: { value: "dirty-trakt" },
    });
    await act(async () => {
      release({
        section: "openSubtitles",
        value: {
          enabled: true,
          apiKey: "••••",
          username: "sam",
          password: "••••",
        },
      });
    });
    expect(calls(fake, "PUT", "/config/opensubtitles")[0]!.body).toMatchObject({
      password: "new-secret",
    });
    expect(calls(fake, "PUT", "/config/trakt")).toHaveLength(0);
    fireEvent.click(screen.getByRole("tab", { name: "OpenSubtitles" }));
    expect(await screen.findByLabelText("Password", {}, WAIT)).toHaveValue("••••");
    fireEvent.click(screen.getByRole("tab", { name: "Trakt" }));
    expect(await screen.findByLabelText("Client ID", {}, WAIT)).toHaveValue("dirty-trakt");
    expect(screen.getByRole("region", { name: "Unsaved changes" })).toBeInTheDocument();
  });

  it("does not let a slow save undo a later card toggle", async () => {
    const releases: Array<(body: unknown) => void> = [];
    const seen: unknown[] = [];
    renderApp("/services/trakt", {}, (f) => {
      f.state.config.trakt = { enabled: true, clientId: "cid", clientSecret: "••••" };
      const orig = f.fetch;
      f.fetch = async (input, init) => {
        const url = new URL(String(input), "http://localhost");
        const path = url.pathname.replace(/^\/ether\/api/, "");
        const method = (init?.method ?? "GET").toUpperCase();
        if (method === "PUT" && path === "/config/trakt") {
          const body = JSON.parse(String(init?.body));
          seen.push(body);
          f.state.calls.push({ method, path, body });
          const value = await new Promise<unknown>((resolve) => {
            releases.push(resolve);
          });
          return new Response(JSON.stringify(value), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        }
        return orig(input, init);
      };
    });
    fireEvent.change(await screen.findByLabelText("Client ID", {}, WAIT), {
      target: { value: "new-id" },
    });
    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    await screen.findByRole("switch", { name: "Trakt enabled" }, WAIT);
    await act(async () => {
      screen.getByRole("button", { name: /Save changes/ }).click();
      screen.getByRole("switch", { name: "Trakt enabled" }).click();
    });
    await waitFor(() => expect(seen).toHaveLength(1), WAIT);
    expect(seen[0]).toMatchObject({ enabled: true, clientId: "new-id", clientSecret: "••••" });
    await act(async () => {
      releases[0]!({
        section: "trakt",
        value: { enabled: true, clientId: "new-id", clientSecret: "••••" },
      });
    });
    await waitFor(() => expect(seen).toHaveLength(2), WAIT);
    expect(seen[1]).toEqual({ enabled: false });
    await act(async () => {
      releases[1]!({
        section: "trakt",
        value: { enabled: false, clientId: "new-id", clientSecret: "••••" },
      });
    });
    await waitFor(
      () => expect(screen.getByRole("switch", { name: "Trakt enabled" })).not.toBeChecked(),
      WAIT,
    );
    expect(seen).toHaveLength(2);
  });

  it("runs the setup wizard from General without clearing a completed server", async () => {
    const { fake } = renderApp("/server");
    fireEvent.click(await screen.findByRole("button", { name: "Run setup wizard" }, WAIT));
    expect(await screen.findByRole("heading", { name: "Set up Ether" }, WAIT)).toBeInTheDocument();
    expect(calls(fake, "POST", "/setup/reopen")).toHaveLength(1);
    expect(calls(fake, "POST", "/setup/complete")).toHaveLength(0);
    expect(fake.state.config.setupComplete).toBe(true);
    expect(fake.state.config.server.friendlyName).toBe("Living Room Ether");
    expect(calls(fake, "PUT", "/config")).toHaveLength(0);
  });

  it("links unfinished setup to the wizard without calling reopen", async () => {
    const { fake } = renderApp("/server", { bootstrap: { setupComplete: false } });
    const link = await screen.findByRole("link", { name: "Run setup wizard" }, WAIT);
    expect(link).toHaveAttribute("href", "/setup");
    expect(screen.queryByRole("button", { name: "Run setup wizard" })).toBeNull();
    fireEvent.click(link);
    expect(await screen.findByRole("heading", { name: "Set up Ether" }, WAIT)).toBeInTheDocument();
    expect(calls(fake, "POST", "/setup/reopen")).toHaveLength(0);
    expect(fake.state.config.setupComplete).toBe(true);
  });

  it("keeps local file playback and crash watching on Maintenance › Health", async () => {
    renderApp("/maintenance/health");
    expect(await screen.findByRole("heading", { name: "Crash watcher" }, WAIT)).toBeVisible();
    expect(
      await screen.findByRole("heading", { name: "Test playback with a local file" }, WAIT),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Serve test files")).toBeInTheDocument();
  });
});
