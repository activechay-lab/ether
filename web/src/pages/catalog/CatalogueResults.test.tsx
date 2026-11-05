import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CatalogueResults, type CatalogueResultsProps } from "./CatalogueResults";
import type { TitleCard } from "../../lib/contract";
const titles = (start: number, count: number): TitleCard[] =>
  Array.from({ length: count }, (_, i) => ({
    key: String(start + i),
    title: `Film ${start + i}`,
    type: "movie",
    year: 1995,
    summary: "A rich story",
    runtimeMinutes: 113,
    genres: ["Thriller"],
  }));
const props = (): CatalogueResultsProps => ({
  items: titles(0, 48),
  list: false,
  page: 1,
  hasMore: false,
  loading: false,
  queryKey: "first",
  onLoadMore: vi.fn(),
  onInspect: vi.fn(),
});
describe("CatalogueResults", () => {
  it("keeps DOM work bounded while accumulating pages and resets on a new query", () => {
    const base = props();
    const { rerender, container } = render(<CatalogueResults {...base} />);
    rerender(<CatalogueResults {...base} page={2} items={titles(48, 48)} />);
    expect(screen.getByRole("button", { name: "Inspect Film 0" })).toBeInTheDocument();
    expect(container.querySelectorAll("article").length).toBeLessThan(40);
    rerender(<CatalogueResults {...base} queryKey="different" items={titles(100, 1)} />);
    expect(screen.getByRole("button", { name: "Inspect Film 100" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Inspect Film 0" })).not.toBeInTheDocument();
  });
  it("offers column choice and keyboard navigation without trapping Tab", async () => {
    const base = props();
    render(<CatalogueResults {...base} list />);
    const first = screen.getByRole("button", { name: "Inspect Film 0" });
    fireEvent.focus(first);
    fireEvent.keyDown(first, { key: "ArrowDown" });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Inspect Film 1" })).toHaveFocus(),
    );
    fireEvent.click(screen.getByText("Columns"));
    fireEvent.click(screen.getByLabelText("Runtime"));
    expect(screen.queryByText("113 min")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Inspect Film 1" }));
    expect(base.onInspect).toHaveBeenCalledWith("1");
  });
  it("adds an explicit selected set to watchlists and exposes failures", async () => {
    const base = props();
    const bulk = vi.fn().mockRejectedValue(new Error("offline"));
    render(<CatalogueResults {...base} items={titles(0, 3)} onBulkWatchlist={bulk} />);
    fireEvent.click(screen.getByLabelText("Select Film 1"));
    fireEvent.click(screen.getByRole("button", { name: "Add to Watchlist" }));
    await screen.findByRole("alert");
    expect(bulk).toHaveBeenCalledWith(["1"]);
    expect(screen.getByText("1 selected")).toBeInTheDocument();
  });
  it("loads the next page at the viewport boundary and offers a manual fallback", () => {
    const base = props();
    render(<CatalogueResults {...base} items={titles(0, 1)} hasMore />);
    expect(base.onLoadMore).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(base.onLoadMore).toHaveBeenCalledTimes(2);
  });
});
