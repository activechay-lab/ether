import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, it, expect, vi } from "vitest";
import { CatalogFilterBar } from "./CatalogFilterBar";
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

describe("AI search action", () => {
  it("passes the original query to the search flow without adding filter chips", () => {
    const search = vi.fn();
    const change = vi.fn();
    render(
      <CatalogFilterBar
        params={new URLSearchParams({ q: "cute whimsical rainy girls night in" })}
        viewers={[]}
        onChange={change}
        aiEnabled
        onAISearch={search}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Search with AI" }));
    expect(search).toHaveBeenCalledWith("cute whimsical rainy girls night in");
    expect(change).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Edit Genre filter" })).not.toBeInTheDocument();
  });
  it("does not offer AI without a configured model", () => {
    render(
      <CatalogFilterBar
        params={new URLSearchParams({ q: "Seven" })}
        viewers={[]}
        onChange={() => {}}
      />,
    );
    expect(screen.queryByRole("button", { name: "Search with AI" })).not.toBeInTheDocument();
  });
});
