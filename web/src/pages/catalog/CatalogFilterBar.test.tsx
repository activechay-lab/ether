import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, it, expect, vi } from "vitest";
import { CatalogFilterBar } from "./CatalogFilterBar";
import { useState } from "react";
import { readQuery } from "./catalogQuery";
describe("catalogue filter URL transactions", () => {
  it("retains a just-applied chip when group matching changes before the router commits", () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ values: [], pending: false }), {
            headers: { "Content-Type": "application/json" },
          }),
      ),
    );
    const initial = new URLSearchParams({
      filters: JSON.stringify({
        mode: "and",
        chips: [{ field: "genre", op: "is", value: "Thriller", group: 1 }],
      }),
      viewer: "7",
      sort: "rating",
    });
    const changes: URLSearchParams[] = [];
    // Intentionally hold parent props constant to model the router's transition
    // delay: both interactions must merge into the most recently requested URL.
    render(
      <MemoryRouter>
        <CatalogFilterBar params={initial} viewers={[]} onChange={(p) => changes.push(p)} />
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Filter" }));
    fireEvent.click(screen.getByRole("button", { name: "Genre" }));
    fireEvent.change(screen.getByLabelText("Value"), { target: { value: "Comedy" } });
    fireEvent.change(screen.getByLabelText("Group"), { target: { value: "1" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    fireEvent.change(screen.getByLabelText("Match within groups"), { target: { value: "or" } });
    const final = changes.at(-1)!;
    expect(readQuery(final.get("filters"))).toMatchObject({
      groupMode: "or",
      chips: [{ value: "Thriller" }, { value: "Comedy" }],
    });
    expect(final.get("viewer")).toBe("7");
    expect(final.get("sort")).toBe("rating");
    fireEvent.change(screen.getByLabelText("Find a title"), { target: { value: "Seven" } });
    expect(readQuery(changes.at(-1)!.get("filters")).chips).toHaveLength(2);
  });
});

function SearchHarness({ query = "" }: { query?: string }) {
  const [params, setParams] = useState(new URLSearchParams(query ? { q: query } : {}));
  return (
    <>
      <CatalogFilterBar params={params} onChange={setParams} viewers={[]} />
      <output data-testid="url">{params.toString()}</output>
    </>
  );
}
describe("unified catalogue search", () => {
  it("keeps descriptions literal when AI is unavailable and has one search input", async () => {
    const fetch = vi.fn(
      async (_url: unknown, _init?: RequestInit) =>
        new Response(JSON.stringify({ enabled: false })),
    );
    vi.stubGlobal("fetch", fetch);
    render(<SearchHarness query="80s comedies over 7" />);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(screen.getAllByRole("textbox")).toHaveLength(1);
    fireEvent.submit(screen.getByLabelText("Find a title").closest("form")!);
    expect(
      screen.queryByRole("button", { name: "Turn search into filters" }),
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText("Find a title")).toHaveValue("80s comedies over 7");
    expect(
      fetch.mock.calls.some((call) => (call[1] as RequestInit | undefined)?.method === "POST"),
    ).toBe(false);
    expect(screen.queryByText("Saved views")).not.toBeInTheDocument();
  });
  it("uses the configured AI automatically for descriptions and manually for literal titles", async () => {
    const fetch = vi.fn(
      async (_url: unknown, init?: RequestInit) =>
        new Response(
          JSON.stringify(
            init?.method === "POST"
              ? { chips: [{ field: "genre", op: "is", value: "Comedy" }] }
              : { enabled: true },
          ),
        ),
    );
    vi.stubGlobal("fetch", fetch);
    const { unmount } = render(<SearchHarness query="80s comedies over 7" />);
    await waitFor(() => expect(screen.getByTestId("url").textContent).toContain("filters="));
    expect(screen.getByLabelText("Find a title")).toHaveValue("");
    expect(screen.getByRole("button", { name: "Edit Genre filter" })).toHaveTextContent("Comedy");
    unmount();
    fetch.mockClear();
    render(<SearchHarness query="Seven" />);
    fireEvent.click(await screen.findByRole("button", { name: "Turn search into filters" }));
    await waitFor(() => expect(screen.getByTestId("url").textContent).toContain("filters="));
    expect(fetch.mock.calls.filter((call) => call[1]?.method === "POST")).toHaveLength(1);
  });
  it("leaves the original search intact when the configured model fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async (_url: unknown, init?: RequestInit) =>
          new Response(
            JSON.stringify(
              init?.method === "POST" ? { error: { message: "unavailable" } } : { enabled: true },
            ),
            { status: init?.method === "POST" ? 502 : 200 },
          ),
      ),
    );
    render(<SearchHarness query="80s comedies over 7" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Search could not be interpreted");
    expect(screen.getByLabelText("Find a title")).toHaveValue("80s comedies over 7");
    expect(screen.getByTestId("url").textContent).not.toContain("filters=");
  });
});
