import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SearchSetting } from "./SearchSetting";
const apiFetch = vi.hoisted(() => vi.fn());
vi.mock("../../../lib/api", () => ({ apiFetch, errorMessage: String }));
vi.mock("../../../i18n/react", () => ({ useLocale: () => ({ locale: "en" }) }));
it("searches without saving and preserves the opaque selected value", async () => {
  const value = JSON.stringify({ name: "Paris, France", latitude: 48.85, longitude: 2.35 });
  apiFetch.mockResolvedValue({ options: [{ value, label: "Paris, France" }] });
  const change = vi.fn();
  render(
    <SearchSetting
      plugin="weather"
      field={{ key: "location", label: "Location" }}
      value=""
      onChange={change}
    />,
  );
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "Paris" } });
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  await waitFor(() =>
    expect(apiFetch).toHaveBeenCalledWith("/plugins/weather/options/location?q=Paris&locale=en"),
  );
  expect(change).not.toHaveBeenCalled();
  fireEvent.click(await screen.findByRole("button", { name: "Search results" }));
  fireEvent.click(await screen.findByRole("option", { name: "Paris, France" }));
  expect(change).toHaveBeenCalledWith(value);
});
