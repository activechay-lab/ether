import { screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import { loadLocale, t } from "../i18n";
import { settingHref } from "../components/SettingFocus";
import { renderApp } from "./harness";

const WAIT = { timeout: 5000 };

beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => {};
});

afterEach(async () => {
  await loadLocale("en");
});

describe("⌘K setting jump", () => {
  it("opens and highlights a setting by key after the language changes", async () => {
    const href = settingHref("/libraries/1/settings", "minPopularity", "browsable");
    expect(href).toContain("#setting=");
    await loadLocale("fr");
    renderApp(href);
    const field = await screen.findByLabelText(t("Minimum popularity"), {}, WAIT);
    await waitFor(() => expect(document.querySelector(".setting-focus")).not.toBeNull(), WAIT);
    expect(document.querySelector(".setting-focus")).toContainElement(field);
  });

  it("highlights a visible setting without opening anything", async () => {
    renderApp(settingHref("/quality/searching", "start-before-the-health-check-finishes"));
    await waitFor(() => expect(document.querySelector(".setting-focus")).not.toBeNull(), WAIT);
    expect(document.querySelector(".setting-focus")).toHaveTextContent(
      "Start before the health check finishes",
    );
    expect(screen.queryByText("Concurrent checks")).toBeNull();
  });
});
