import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { XtreamLogins } from "../pages/apps/XtreamLogins";
import { copyText } from "../components/CopyButton";
const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("../lib/api", () => ({ apiFetch: request, errorMessage: () => "Request failed" }));
vi.mock("../components/pickers", () => ({
  loadViewers: async () => [{ id: 1, title: "Alex", username: "alex" }],
  viewerName: (v: { title: string }) => v.title,
}));
beforeEach(() => {
  request.mockReset();
});
it("creates and revokes a viewer-scoped login inline", async () => {
  request.mockImplementation(async (path: string, options?: { method?: string }) => {
    if (path === "/viewers/1/apps/xtream")
      return { url: "http://ether.test/xtream", username: "xt_mock", password: "mock-secret" };
    if (path === "/users/1/devices/revoke") return { ok: true };
    if (path === "/users/1/devices")
      return { devices: options ? [] : [{ hash: "hash", surface: "xtream" }] };
    throw Error(path);
  });
  render(<XtreamLogins />);
  await screen.findByRole("option", { name: "Alex" });
  fireEvent.change(screen.getByRole("combobox", { name: "Viewer" }), { target: { value: "1" } });
  fireEvent.click(screen.getByRole("button", { name: "Create connection" }));
  await screen.findByText("mock-secret");
  expect(screen.getByText("http://ether.test/xtream")).toBeVisible();
  fireEvent.click(await screen.findByRole("button", { name: "Revoke" }));
  await waitFor(() => expect(screen.queryByText("mock-secret")).not.toBeInTheDocument());
  expect(request).toHaveBeenCalledWith("/users/1/devices/revoke", {
    method: "POST",
    body: JSON.stringify({ hash: "hash" }),
  });
});
it("copies on HTTP without navigator clipboard", async () => {
  Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
  const command = vi.fn(() => true);
  Object.defineProperty(document, "execCommand", { value: command, configurable: true });
  expect(await copyText("mock-secret")).toBe(true);
  expect(command).toHaveBeenCalledWith("copy");
  expect(document.querySelector("textarea")).toBeNull();
});
