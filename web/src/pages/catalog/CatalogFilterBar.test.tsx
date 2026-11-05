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
