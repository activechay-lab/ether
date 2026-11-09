import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { loadLocale, t } from "../i18n";
import { I18nProvider, useLocale } from "../i18n/react";
import { createFakeServer } from "../mock/server";
import { TEMPLATES, TEMPLATE_BUNDLES } from "../mock/templates";
import CollectionsGallery from "../pages/collections/Gallery";
import { StarterBundlesCard } from "../pages/setup/StarterBundlesCard";

function SwitchLanguage() {
  const { setLocale } = useLocale();
  return <button onClick={() => void setLocale("fr")}>French</button>;
}

afterEach(async () => {
  cleanup();
  await loadLocale("en");
});

it("switches template copy in place and searches translated names and descriptions", async () => {
  const fake = createFakeServer();
  vi.stubGlobal("fetch", vi.fn(fake.fetch));
  render(
    <MemoryRouter initialEntries={["/collections?tab=templates"]}>
      <I18nProvider>
        {() => (
          <>
            <SwitchLanguage />
            <CollectionsGallery />
          </>
        )}
      </I18nProvider>
    </MemoryRouter>,
  );
  const template = TEMPLATES.find((item) => item.id === "mood-edge-of-your-seat")!;
  expect(
    await screen.findByRole("button", { name: `Preview template ${template.name}` }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "French" }));
  await waitFor(() => expect(t(template.name)).not.toBe(template.name));
  const name = t(template.name);
  fireEvent.change(screen.getByRole("searchbox", { name: t("Search collections") }), {
    target: { value: name },
  });
  expect(
    screen.getByRole("button", { name: t("Preview template {name}", { name }) }),
  ).toBeInTheDocument();
  expect(screen.getByRole("heading", { name })).toBeInTheDocument();
  expect(screen.getByText(t(template.description))).toBeInTheDocument();
  expect(
    screen.getByText(t("{category} template", { category: t(template.category) })),
  ).toBeInTheDocument();
  fireEvent.change(screen.getByRole("searchbox", { name: t("Search collections") }), {
    target: { value: t(template.description) },
  });
  expect(
    screen.getByRole("button", { name: t("Preview template {name}", { name }) }),
  ).toBeInTheDocument();
  expect(fake.state.collections.find((item) => item.templateId === template.id)?.name).toBe(
    template.spec.name,
  );
});

it("renders setup bundles and their real template previews in the selected language", async () => {
  await loadLocale("fr");
  const fake = createFakeServer();
  vi.stubGlobal("fetch", vi.fn(fake.fetch));
  render(<StarterBundlesCard />);
  for (const bundle of TEMPLATE_BUNDLES) {
    expect(await screen.findByText(t(bundle.name))).toBeInTheDocument();
    expect(screen.getByText(t(bundle.description))).toBeInTheDocument();
  }
  fireEvent.click(screen.getAllByRole("button", { name: t("Preview bundle") })[0]!);
  const first = TEMPLATES.find((item) => item.id === TEMPLATE_BUNDLES[0]!.templateIds[0])!;
  expect(await screen.findByText(t(first.name))).toBeInTheDocument();
});
