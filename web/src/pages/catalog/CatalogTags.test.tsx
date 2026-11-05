import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CatalogTags } from "./CatalogTags";
import { apiFetch } from "../../lib/api";
vi.mock("../../lib/api", () => ({ apiFetch: vi.fn() }));

describe("catalogue owner tags", () => {
  it("edits a bounded annotation list without changing provider keywords", async () => {
    let tags = ["favourites"];
    vi.mocked(apiFetch).mockImplementation(async (_path, options) => {
      if (options?.method === "PUT") tags = JSON.parse(options.body as string).tags;
      return { tags } as never;
    });
    render(<CatalogTags titleKey="1000000000000007" />);
    await screen.findByText("favourites");
    fireEvent.click(screen.getByRole("button", { name: "Edit", exact: true }));
    fireEvent.change(screen.getByRole("textbox", { name: "Tags" }), {
      target: { value: "Friday, family, Friday" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save", exact: true }));
    await waitFor(() => expect(tags).toEqual(["Friday", "family"]));
    expect(await screen.findByText("Friday")).toBeInTheDocument();
  });
});
