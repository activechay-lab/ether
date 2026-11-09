import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TitleSearch } from "../components/pickers";
import { renderAppWith } from "./routes";
import { calls } from "./harness";

const hiddenMovie = {
  key: "1000000000000501",
  title: "Quiet Harbor",
  type: "movie" as const,
  year: 2021,
  genres: ["Drama"],
  rating: 7.4,
};

const gatedMovie = {
  key: "1000000000000502",
  title: "Premiere Night",
  type: "movie" as const,
  year: 2026,
  genres: ["Thriller"],
  rating: 7.9,
};

describe("Catalog library explanations, overrides, and search affordances", () => {
  it("labels Find on TMDB results as Open, explains why a title is excluded, and adds/removes it", async () => {
    let included = false;
    const { fake } = renderAppWith("/catalog", (method, path, _body, query) => {
      if (path === "/catalog/browse") {
        return {
          items: included ? [hiddenMovie] : [],
          total: included ? 1 : 0,
          totalExact: true,
        };
      }
      if (path === "/catalog/search") {
        return {
          items: query.get("all") === "1" ? [hiddenMovie] : [],
        };
      }
      if (path === `/catalog/title/${hiddenMovie.key}`) {
        return {
          ...hiddenMovie,
          summary: "An overlooked indie drama.",
          status: "Released",
        };
      }
      if (path === `/catalog/title/${hiddenMovie.key}/library`) {
        if (method === "PUT") included = true;
        if (method === "DELETE") included = false;
        return included
          ? {
              browsable: true,
              included: true,
              inLibraryTier: true,
              canInclude: false,
              releaseGated: false,
              reasons: [],
              items: [],
            }
          : {
              browsable: false,
              included: false,
              inLibraryTier: false,
              canInclude: true,
              releaseGated: false,
              reasons: [
                "Below your popularity floor (12 votes; floor 50)",
                "Excluded by your catalogue rule “No Slow Cinema”",
              ],
              items: [
                {
                  code: "popularity_floor",
                  message: "Below your popularity floor (12 votes; floor 50)",
                  votes: 12,
                  minVotes: 50,
                },
                {
                  code: "catalog_rule_named",
                  message: "Excluded by your catalogue rule “No Slow Cinema”",
                  rule: "No Slow Cinema",
                },
              ],
            };
      }
      return undefined;
    });

    const findBtn = await screen.findByRole("button", { name: "Find on TMDB" }, { timeout: 15000 });
    fireEvent.click(findBtn);

    const lookupDrawer = await screen.findByRole("dialog", { name: "Find a title on TMDB" });
    fireEvent.change(within(lookupDrawer).getByRole("textbox", { name: "Search titles" }), {
      target: { value: "Quiet" },
    });

    const results = await within(lookupDrawer).findByRole("list", { name: "Search results" });
    const rowBtn = within(results).getByRole("button");
    expect(rowBtn).toHaveAttribute("title", "Open title details");
    expect(within(rowBtn).getByText("Open")).toBeInTheDocument();
    expect(within(rowBtn).queryByText("Add")).not.toBeInTheDocument();
    expect(calls(fake, "GET", "/catalog/search").at(-1)?.path).toContain("all=1");

    fireEvent.click(rowBtn);

    const detail = await screen.findByRole("dialog", { name: "Quiet Harbor" });
    expect(await within(detail).findByText("Not in your library")).toBeInTheDocument();
    expect(
      within(detail).getByText("Below your popularity floor (12 votes; floor 50)"),
    ).toBeInTheDocument();
    expect(
      within(detail).getByText("Excluded by your catalogue rule “No Slow Cinema”"),
    ).toBeInTheDocument();

    fireEvent.click(within(detail).getByRole("button", { name: "Add to library" }));
    expect(await within(detail).findByText("Added to library")).toBeInTheDocument();
    expect(
      within(detail).getByText("Included in your browsable library by you."),
    ).toBeInTheDocument();
    expect(calls(fake, "PUT", `/catalog/title/${hiddenMovie.key}/library`)).toHaveLength(1);

    fireEvent.click(within(detail).getByRole("button", { name: "Remove from library" }));
    expect(await within(detail).findByText("Not in your library")).toBeInTheDocument();
    expect(calls(fake, "DELETE", `/catalog/title/${hiddenMovie.key}/library`)).toHaveLength(1);
  });

  it("explains release-gated new movies without offering Add to library", async () => {
    renderAppWith(`/catalog?title=${gatedMovie.key}`, (_method, path) => {
      if (path === "/catalog/browse") return { items: [], total: 0 };
      if (path === `/catalog/title/${gatedMovie.key}`) {
        return { ...gatedMovie, status: "Released" };
      }
      if (path === `/catalog/title/${gatedMovie.key}/library`) {
        return {
          browsable: false,
          included: false,
          inLibraryTier: true,
          canInclude: false,
          releaseGated: true,
          reasons: ["Waiting for a playable release (new movie)"],
          items: [
            {
              code: "release_gate_movie",
              message: "Waiting for a playable release (new movie)",
            },
          ],
        };
      }
      return undefined;
    });

    const detail = await screen.findByRole("dialog", { name: "Premiere Night" });
    expect(await within(detail).findByText("Not in your library")).toBeInTheDocument();
    expect(
      within(detail).getByText("Waiting for a playable release (new movie)"),
    ).toBeInTheDocument();
    expect(
      within(detail).getByText(
        "This title will appear automatically once a playable release exists.",
      ),
    ).toBeInTheDocument();
    expect(
      within(detail).queryByRole("button", { name: "Add to library" }),
    ).not.toBeInTheDocument();
  });

  it("distinguishes Add vs Select affordances in TitleSearch", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ items: [hiddenMovie] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      ),
    );

    const { unmount } = render(<TitleSearch onPick={() => {}} label="Single picker" />);
    fireEvent.change(screen.getByRole("textbox", { name: "Single picker" }), {
      target: { value: "Quiet" },
    });
    const selectList = await screen.findByRole("list", { name: "Search results" });
    expect(within(selectList).getByRole("button")).toHaveAttribute("title", "Select title");
    expect(within(selectList).getByText("Select")).toBeInTheDocument();
    unmount();

    render(<TitleSearch onPick={() => {}} excludeKeys={new Set()} label="List builder" />);
    fireEvent.change(screen.getByRole("textbox", { name: "List builder" }), {
      target: { value: "Quiet" },
    });
    await waitFor(() => expect(screen.getByRole("list", { name: "Search results" })).toBeTruthy());
    const addList = screen.getByRole("list", { name: "Search results" });
    expect(within(addList).getByRole("button")).toHaveAttribute("title", "Add to list");
    expect(within(addList).getByText("Add")).toBeInTheDocument();
  });
});
