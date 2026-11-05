import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { extract } from "../../scripts/i18n-lib.mjs";
import { api } from "../lib/api";
import { descriptors } from "../mock/extend/descriptors/settings";
import { createFakeServer } from "../mock/server";
import {
  SchemaGroups,
  fieldGroupsFromServed,
  type LoadOptions,
  type ServedSchema,
} from "../pages/settings/SchemaForm";

const WAIT = { timeout: 4000 };

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("served settings schemas", () => {
  it("keeps mock settingsSchema descriptors identical to Go builtins.json", () => {
    const goldenPath = path.resolve(
      __dirname,
      "../../../internal/config/schema/testdata/builtins.json",
    );
    const goBuiltins = JSON.parse(readFileSync(goldenPath, "utf8")) as {
      id: string;
      configKey?: string;
      label: { source: string };
      schema: ServedSchema;
    }[];
    const fromGo = goBuiltins.map((b, order) => ({
      kind: "settingsSchema",
      id: b.id,
      owner: "builtin",
      label: b.label,
      order,
      meta: {
        ...(b.configKey ? { configKey: b.configKey } : {}),
        schema: b.schema,
      },
    }));
    expect(descriptors).toEqual(fromGo);
  });

  it("extracts extend.T string literals from Go and flags non-literal arguments", () => {
    const dir = mkdtempSync(path.join(tmpdir(), "ether-i18n-go-"));
    try {
      const sampleFile = path.join(dir, "sample.go");
      writeFileSync(
        sampleFile,
        [
          "package sample",
          'import ext "github.com/braedonsaunders/ether/internal/extend"',
          '// extend.T("ignored in line comment")',
          '/* extend.T("ignored in block comment") */',
          'const s = "extend.T(\\"ignored in string\\")"',
          'var a = extend.T("Server name")',
          "var b = ext.T(`Raw copy`)",
          "var bad = extend.T(dynamicLabel)",
        ].join("\n"),
      );
      const scanned = extract([sampleFile]);
      expect([...scanned.messages.keys()]).toEqual(["Server name", "Raw copy"]);
      expect(scanned.dynamic).toHaveLength(1);
      expect(scanned.dynamic[0]).toMatch(/sample\.go:8$/);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }

    const catalogue = extract();
    expect(
      catalogue.messages
        .get("Server name")
        ?.refs.some((r) => r.startsWith("internal/config/schema/")),
    ).toBe(true);
    expect(catalogue.dynamic.filter((r) => r.startsWith("internal/"))).toEqual([]);
  });

  it("renders and edits every field kind in SchemaGroups", async () => {
    const fake = createFakeServer();
    vi.stubGlobal("fetch", vi.fn(fake.fetch));

    const viewers = await api.viewers();
    const firstViewer = viewers[0]!;
    const titles = await api.searchCatalog("th");
    const firstTitle = titles[0]!;

    const t = (source: string) => ({ source });
    const schema: ServedSchema = {
      groups: [
        {
          id: "all",
          title: t("All field kinds"),
          description: t("Exercises every settings schema field editor."),
          fields: [
            { key: "textField", label: t("Text field"), kind: "text", placeholder: "hello" },
            { key: "secretField", label: t("Secret field"), kind: "secret" },
            { key: "durationField", label: t("Duration field"), kind: "duration" },
            { key: "readonlyField", label: t("Readonly field"), kind: "readonly" },
            {
              key: "numberField",
              label: t("Number field"),
              kind: "number",
              min: 0,
              max: 100,
              step: 5,
              unit: t("kbps"),
            },
            { key: "boolField", label: t("Bool field"), kind: "bool" },
            {
              key: "selectField",
              label: t("Select field"),
              kind: "select",
              options: [
                { value: "alpha", label: t("Alpha") },
                { value: "beta", label: t("Beta") },
              ],
            },
            {
              key: "dynamicSelect",
              label: t("Dynamic select"),
              kind: "select",
              optionsFrom: "qualities",
              numeric: true,
            },
            { key: "listField", label: t("List field"), kind: "list" },
            { key: "bytesField", label: t("Bytes field"), kind: "bytes", unit: t("GiB") },
            { key: "jsonField", label: t("JSON field"), kind: "json" },
            { key: "hiddenField", label: t("Hidden field"), kind: "hidden" },
            {
              key: "multiSelectField",
              label: t("Multi-select field"),
              kind: "multiSelect",
              options: [
                { value: "one", label: t("One") },
                { value: "two", label: t("Two") },
                { value: "three", label: t("Three") },
              ],
            },
            {
              key: "orderedListField",
              label: t("Ordered list field"),
              kind: "orderedList",
              options: [
                { value: "remux", label: t("Remux") },
                { value: "bluray", label: t("Blu-ray") },
                { value: "webdl", label: t("WEB-DL") },
              ],
            },
            { key: "languageField", label: t("Language field"), kind: "language" },
            { key: "viewerField", label: t("Viewer field"), kind: "viewer" },
            { key: "collectionField", label: t("Collection field"), kind: "collection" },
            { key: "folderField", label: t("Folder field"), kind: "folder", wide: true },
            { key: "urlField", label: t("URL field"), kind: "url" },
            { key: "titleField", label: t("Title field"), kind: "title" },
            {
              key: "nestedGroup",
              label: t("Nested group"),
              kind: "group",
              fields: [{ key: "innerText", label: t("Inner text"), kind: "text" }],
            },
          ],
          more: [{ key: "moreText", label: t("Advanced text"), kind: "text" }],
        },
      ],
    };

    const loadOptions: LoadOptions = vi.fn(async (field: string) => {
      if (field === "qualities") {
        return [
          { value: "720", label: "720p" },
          { value: "1080", label: "1080p" },
        ];
      }
      return [];
    });

    let latest: Record<string, unknown> = {
      textField: "initial",
      secretField: "••••",
      durationField: "15m",
      readonlyField: "machine-123",
      numberField: 20,
      boolField: false,
      selectField: "alpha",
      dynamicSelect: 720,
      listField: ["a"],
      bytesField: 2 * 1024 ** 3,
      jsonField: { a: 1 },
      hiddenField: "stays-hidden",
      multiSelectField: ["one"],
      orderedListField: ["remux", "bluray"],
      languageField: "en",
      viewerField: "",
      collectionField: "",
      folderField: "/mnt/media",
      urlField: "https://example.org",
      titleField: "",
      nestedGroup: { innerText: "inside" },
      moreText: "power",
    };

    function Harness({ errors }: { errors?: Record<string, string> }) {
      const [val, setVal] = useState<Record<string, unknown>>(latest);
      return (
        <SchemaGroups
          groups={fieldGroupsFromServed(schema)}
          prefix="settings"
          value={val}
          errors={errors ?? {}}
          loadOptions={loadOptions}
          onChange={(next) => {
            latest = next;
            setVal(next);
          }}
        />
      );
    }

    const { rerender } = render(<Harness />);

    // Hidden field never renders; readonly renders as code.
    expect(screen.queryByLabelText("Hidden field")).toBeNull();
    expect(screen.getByText("machine-123")).toBeInTheDocument();

    // Text, duration, folder, url, nested group text.
    fireEvent.change(screen.getByLabelText("Text field"), { target: { value: "updated" } });
    expect(latest.textField).toBe("updated");

    // Durations are an amount plus a unit.
    fireEvent.change(screen.getByLabelText("Duration field"), { target: { value: "2" } });
    expect(latest.durationField).toBe("2m");
    fireEvent.click(screen.getByRole("button", { name: "Duration field unit" }));
    fireEvent.click(await screen.findByRole("option", { name: "Hours" }));
    expect(latest.durationField).toBe("2h");

    fireEvent.change(screen.getByLabelText("Folder field"), { target: { value: "/srv/video" } });
    expect(latest.folderField).toBe("/srv/video");

    fireEvent.change(screen.getByLabelText("URL field"), {
      target: { value: "https://ether.example/manifest.json" },
    });
    expect(latest.urlField).toBe("https://ether.example/manifest.json");

    fireEvent.change(screen.getByLabelText("Inner text"), { target: { value: "deep" } });
    expect(latest.nestedGroup).toEqual({ innerText: "deep" });

    // Secret field.
    fireEvent.change(screen.getByLabelText("Secret field"), { target: { value: "new-secret" } });
    expect(latest.secretField).toBe("new-secret");

    // Bool switch.
    fireEvent.click(screen.getByRole("switch", { name: "Bool field" }));
    expect(latest.boolField).toBe(true);

    // Number and Bytes (GiB).
    const numInput = screen.getByLabelText("Number field");
    fireEvent.change(numInput, { target: { value: "45" } });
    fireEvent.blur(numInput);
    expect(latest.numberField).toBe(45);

    const bytesInput = screen.getByLabelText("Bytes field in GiB");
    fireEvent.change(bytesInput, { target: { value: "5" } });
    fireEvent.blur(bytesInput);
    expect(latest.bytesField).toBe(5 * 1024 ** 3);

    // List and JSON fields.
    const listInput = screen.getByLabelText("List field");
    fireEvent.change(listInput, { target: { value: "x, y" } });
    fireEvent.blur(listInput);
    expect(latest.listField).toEqual(["x", "y"]);

    const jsonInput = screen.getByLabelText("JSON field");
    fireEvent.change(jsonInput, { target: { value: '{"a":2}' } });
    fireEvent.blur(jsonInput);
    expect(latest.jsonField).toEqual({ a: 2 });

    // Select (static) and Select (runtime optionsFrom + numeric).
    fireEvent.click(screen.getByRole("button", { name: "Select field" }));
    fireEvent.click(within(await screen.findByRole("listbox", { hidden: true })).getByText("Beta"));
    expect(latest.selectField).toBe("beta");

    await waitFor(() => expect(loadOptions).toHaveBeenCalledWith("qualities"), WAIT);
    fireEvent.click(screen.getByRole("button", { name: "Dynamic select" }));
    fireEvent.click(
      within(await screen.findByRole("listbox", { hidden: true })).getByText("1080p"),
    );
    expect(latest.dynamicSelect).toBe(1080);

    // MultiSelect.
    fireEvent.click(screen.getByLabelText("Multi-select field"));
    fireEvent.click(await screen.findByText("Two", {}, WAIT));
    expect(latest.multiSelectField).toEqual(["one", "two"]);
    fireEvent.keyDown(document, { key: "Escape" });

    // OrderedList: remove an item and add one back from the SearchSelect.
    fireEvent.click(screen.getByRole("button", { name: "Remove Remux" }));
    expect(latest.orderedListField).toEqual(["bluray"]);
    fireEvent.click(screen.getByRole("button", { name: "Add to Ordered list field" }));
    fireEvent.click(
      within(await screen.findByRole("listbox", { hidden: true })).getByText("WEB-DL"),
    );
    expect(latest.orderedListField).toEqual(["bluray", "webdl"]);

    // Language picker.
    fireEvent.click(screen.getByRole("button", { name: "Language field" }));
    const langListbox = await screen.findByRole("listbox", { hidden: true });
    fireEvent.click(within(langListbox).getByText(/\(fr\)$/));
    expect(latest.languageField).toBe("fr");

    // Viewer picker (loaded from /api/viewers).
    const viewerLabel = firstViewer.title || firstViewer.username;
    await waitFor(() => {
      fireEvent.click(screen.getByRole("button", { name: "Viewer field" }));
      expect(screen.getByText(viewerLabel)).toBeInTheDocument();
    }, WAIT);
    fireEvent.click(
      within(await screen.findByRole("listbox", { hidden: true })).getByText(viewerLabel),
    );
    expect(String(latest.viewerField)).toBe(String(firstViewer.id));

    // Collection picker (loaded from /api/collections).
    const firstCol = fake.state.collections[0]!;
    await waitFor(() => {
      fireEvent.click(screen.getByRole("button", { name: "Collection field" }));
      expect(screen.getByText(firstCol.name)).toBeInTheDocument();
    }, WAIT);
    fireEvent.click(
      within(await screen.findByRole("listbox", { hidden: true })).getByText(firstCol.name),
    );
    expect(latest.collectionField).toBe(firstCol.id);

    // Title picker (TitleSearch backed by /api/catalog/search).
    const titleBox = screen.getByRole("textbox", { name: "Title field" });
    fireEvent.change(titleBox, { target: { value: "th" } });
    const results = await screen.findByRole("list", { name: "Search results" }, WAIT);
    fireEvent.click(within(results).getByText(firstTitle.title));
    expect(latest.titleField).toBe(firstTitle.key);
    fireEvent.click(screen.getByRole("button", { name: "Clear Title field" }));
    expect(latest.titleField).toBe("");

    // More options: collapsed initially, auto-opens when a JSON-pointer error targets a `more` field.
    expect(screen.queryByLabelText("Advanced text")).toBeNull();
    rerender(
      <Harness errors={{ "/moreText": "Must be text.", "/nestedGroup/innerText": "Bad" }} />,
    );
    expect(await screen.findByLabelText("Advanced text")).toBeInTheDocument();
    expect(screen.getByText("Must be text.")).toBeInTheDocument();
    expect(screen.getByText("Bad")).toBeInTheDocument();
  });
});
