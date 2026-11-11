import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "../App";
import "../pages/libraries/Libraries";
import { createFakeServer } from "../mock/server";
import { calls, renderApp, settleUi } from "./harness";
import { renderAppWith } from "./routes";

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] });
});

describe("Library visibility", () => {
  it.each(["hidden", "shown"])(
    "browses %s rules with the shared catalog search",
    async (visibility) => {
      const { fake } = renderAppWith("/libraries/1/rules", (_method, path) => {
        if (path === "/catalog/visibility/preview")
          return {
            total: 1000,
            hidden: 400,
            visible: 600,
            movies: 400,
            shows: 0,
            items: Array.from({ length: 24 }, (_, i) => ({
              key: `hidden-${i}`,
              title: `Hidden ${i}`,
            })),
            shown: Array.from({ length: 24 }, (_, i) => ({
              key: `shown-${i}`,
              title: `Shown ${i}`,
            })),
          };
      });
      await settleUi(() =>
        expect(screen.getAllByRole("button", { name: "Browse" })).toHaveLength(2),
      );
      fireEvent.click(
        screen.getAllByRole("button", { name: "Browse" })[visibility === "hidden" ? 0 : 1]!,
      );
      await settleUi(() =>
        expect(screen.getByPlaceholderText("Search catalog…")).toBeInTheDocument(),
      );
      fireEvent.change(screen.getByPlaceholderText("Search catalog…"), {
        target: { value: "Dune" },
      });
      await settleUi(() => {
        const requests = fake.state.calls.filter((call) => call.path.startsWith("/catalog/browse"));
        expect(
          requests.some((call) => {
            const params = new URL(call.path, "http://localhost").searchParams;
            return (
              params.get("libraryId") === "1" &&
              params.get("ruleVisibility") === visibility &&
              params.get("q") === "Dune" &&
              params.has("rules")
            );
          }),
        ).toBe(true);
      });
    },
  );

  it.each([null, undefined])("keeps empty previews usable when items is %s", async (items) => {
    const { fake } = renderAppWith("/libraries/1/rules", (_method, path) => {
      if (path === "/catalog/visibility/preview")
        return { total: 63996, hidden: 0, movies: 0, shows: 0, unknownCountry: 16721, items };
    });
    await settleUi(() => {
      expect(screen.getByText("63,996")).toBeInTheDocument();
      expect(screen.getByText("No rules yet. Everything is shown.")).toBeInTheDocument();
    });
    expect(screen.getByText("Hidden · 0")).toBeInTheDocument();
    expect(screen.getByText("titles visible")).toBeInTheDocument();
    expect(screen.getByText("Nothing hidden")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Browse" })[0]).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    expect(screen.getByLabelText("Rule name")).toHaveValue("");
    expect(screen.queryByRole("radiogroup", { name: "Rule media" })).not.toBeInTheDocument();
    expect(calls(fake, "PATCH", "/libraries/1")).toHaveLength(0);
  });

  it("previews stored rules on the library and saves via PATCH /libraries/:id", async () => {
    const exclusions = [
      {
        name: "Country boundary",
        mediaType: "movie",
        match: "all",
        rules: [{ field: "country", op: "in", value: ["IN"] }],
      },
    ];
    const fake = createFakeServer();
    (fake.state.config.catalog as { exclusions?: unknown }).exclusions = exclusions;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "http://localhost");
        const path = url.pathname.replace(/^\/ether\/api/, "");
        const method = (init?.method ?? "GET").toUpperCase();
        let body: unknown;
        if (typeof init?.body === "string" && init.body) body = JSON.parse(init.body);
        if (method === "POST" && path === "/catalog/visibility/preview") {
          const n = ((body as { exclusions?: unknown[] }).exclusions ?? []).length;
          fake.state.calls.push({ method, path: path + url.search, body });
          return new Response(
            JSON.stringify({
              total: 35000,
              hidden: n ? 800 : 0,
              movies: n ? 800 : 0,
              shows: 0,
              rules: n ? [{ movies: 800, shows: 0 }] : [],
              items: [],
              shown: [],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        return fake.fetch(input, init);
      }),
    );
    render(
      <MemoryRouter initialEntries={["/libraries/1/rules"]}>
        <App />
      </MemoryRouter>,
    );
    await settleUi(() => {
      expect(screen.getByText(/Hide · Country is IN/)).toBeInTheDocument();
      expect(fake.state.calls.filter((c) => c.path.includes("preview")).length).toBeGreaterThan(0);
      expect(screen.getByText("Hidden: 800 movies")).toBeInTheDocument();
      expect(screen.getByText(/^visible of /)).toBeInTheDocument();
      expect(screen.getByText("800 movies")).toBeInTheDocument();
    });
    expect(fake.state.calls.find((c) => c.path.includes("preview"))?.body).toMatchObject({
      libraryId: 1,
    });
    fireEvent.click(screen.getByRole("button", { name: "Delete rule 1" }));
    await settleUi(() => {
      expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled();
    });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await settleUi(() => {
      expect(calls(fake, "PATCH", "/libraries/1")).toHaveLength(1);
    });
    expect(calls(fake, "PATCH", "/libraries/1")[0]?.body).toMatchObject({
      rules: [],
      visibilityDefault: "show",
    });
  });

  it("never shows stored rules as empty while adopting them", async () => {
    let flashedEmpty = false;
    const observer = new MutationObserver(() => {
      flashedEmpty ||=
        document.body.textContent?.includes("No rules yet. Everything is shown.") ?? false;
    });
    observer.observe(document.body, { childList: true, subtree: true, characterData: true });
    renderApp("/libraries/1/rules", {}, (server) => {
      (server.state.config.catalog as { exclusions?: unknown }).exclusions = [
        {
          name: "Stored group",
          mediaType: "movie",
          match: "all",
          rules: [{ field: "country", op: "in", value: ["IN"] }],
        },
      ];
    });
    await settleUi(() => {
      expect(screen.getByText(/Hide · Country is IN/)).toBeInTheDocument();
    });
    observer.disconnect();
    expect(flashedEmpty).toBe(false);
  });

  it("keeps a compiler rejection out of the preview", async () => {
    const fake = createFakeServer();
    (fake.state.config.catalog as { exclusions?: unknown }).exclusions = [
      {
        name: "",
        action: "hide",
        mediaType: "movie",
        match: "all",
        rules: [{ field: "genre", op: "in", value: ["Horror"] }],
      },
    ];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "http://localhost");
        const path = url.pathname.replace(/^\/ether\/api/, "");
        const method = (init?.method ?? "GET").toUpperCase();
        if (method === "POST" && path === "/catalog/visibility/preview") {
          return new Response(
            JSON.stringify({
              error: {
                code: "invalid_filter",
                message: "group 2 needs a name of 1–120 characters",
              },
            }),
            { status: 400, headers: { "Content-Type": "application/json" } },
          );
        }
        return fake.fetch(input, init);
      }),
    );
    render(
      <MemoryRouter initialEntries={["/libraries/1/rules"]}>
        <App />
      </MemoryRouter>,
    );
    await settleUi(() => {
      expect(screen.getByText(/Hide · Genre is Horror/)).toBeInTheDocument();
      expect(screen.getByText("Preview isn't available.")).toBeInTheDocument();
    });
    expect(screen.queryByText(/needs a name/)).not.toBeInTheDocument();
    expect(screen.queryByText("Something went wrong")).not.toBeInTheDocument();
    expect(screen.queryByText(/0 titles visible/)).not.toBeInTheDocument();
  });

  it("does not save an unfinished rule", async () => {
    const { fake } = renderApp("/libraries/1/rules");
    await settleUi(() => {
      expect(screen.getByText("No rules yet. Everything is shown.")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    expect(screen.getByLabelText("Rule name")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
    expect(calls(fake, "PATCH", "/libraries/1")).toHaveLength(0);
  });

  it("shows a stored film-and-TV allowlist as rating levels", async () => {
    renderApp("/libraries/1/rules", {}, (server) => {
      (server.state.config.catalog as { exclusions?: unknown }).exclusions = [
        {
          name: "Family",
          mediaType: "mixed",
          match: "all",
          rules: [{ field: "contentRating", op: "notIn", value: ["G", "PG", "TV-G", "TV-PG"] }],
        },
      ];
    });
    await settleUi(() => {
      expect(screen.getByText(/Hide · Content rating is/)).toBeInTheDocument();
      expect(screen.getByText(/G · TV-G · TV-Y/)).toBeInTheDocument();
      expect(screen.getByText(/PG · TV-PG/)).toBeInTheDocument();
    });
    expect(screen.getByText(/PG · TV-PG · TV-Y7/)).toBeInTheDocument();
    expect(screen.queryByText("TV-PG")).not.toBeInTheDocument();
    expect(screen.getByText(/Ratings match by age level/)).toBeInTheDocument();
  });

  it("limits a hide rule to browsing and saves its scope", async () => {
    const { fake } = renderApp("/libraries/1/rules", {}, (server) => {
      (server.state.config.catalog as { exclusions?: unknown }).exclusions = [
        {
          name: "",
          action: "hide",
          mediaType: "movie",
          match: "all",
          rules: [{ field: "country", op: "notIn", value: ["US"] }],
        },
      ];
    });
    await settleUi(() => {
      expect(screen.getByRole("button", { name: "Edit rule 1" })).toBeInTheDocument();
    });
    expect(screen.queryByText(/Browsing only/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit rule 1" }));
    const where = screen.getByRole("button", { name: "Where to hide" });
    expect(where).toHaveTextContent("Hide everywhere");
    expect(screen.getByText(/Anyone can still search for them and play them/)).toBeInTheDocument();
    fireEvent.click(where);
    await settleUi(() => {
      expect(screen.getByRole("option", { name: "Hide from browsing only" })).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole("option", { name: "Hide from browsing only" }));
    expect(screen.getByRole("button", { name: "Where to hide" })).toHaveTextContent(
      "Hide from browsing only",
    );
    await settleUi(() => {
      expect(screen.getAllByText(/· Browsing only/).length).toBeGreaterThan(0);
    });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await settleUi(() => {
      expect(calls(fake, "PATCH", "/libraries/1")).toHaveLength(1);
    });
    expect(calls(fake, "PATCH", "/libraries/1")[0]?.body).toMatchObject({
      rules: [{ action: "hide", scope: "discovery" }],
    });
  });

  it("offers the browsing-only choice for hide rules alone", async () => {
    renderApp("/libraries/1/rules", {}, (server) => {
      (server.state.config.catalog as { exclusions?: unknown }).exclusions = [
        {
          name: "",
          action: "hide",
          scope: "discovery",
          mediaType: "movie",
          match: "all",
          rules: [{ field: "country", op: "notIn", value: ["US"] }],
        },
      ];
    });
    await settleUi(() => {
      expect(screen.getByText(/· Browsing only/)).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole("button", { name: "Edit rule 1" }));
    expect(screen.getByRole("button", { name: "Where to hide" })).toHaveTextContent(
      "Hide from browsing only",
    );
    fireEvent.click(
      within(screen.getByRole("radiogroup", { name: "Rule action" })).getByRole("radio", {
        name: "Show",
      }),
    );
    await settleUi(() => {
      expect(screen.queryByRole("button", { name: "Where to hide" })).not.toBeInTheDocument();
    });
  });
});
