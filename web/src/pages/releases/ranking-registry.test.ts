import { describe, expect, it } from "vitest";
import { descriptors } from "../../mock/extend/descriptors/ranking";
import { defaultPreferScore, withRuleScores } from "../../lib/qualityRanking";
import {
  descriptorLabel,
  descriptorOwnerName,
  newCondition,
  resolveRuleFieldMeta,
  type RuleFieldDescriptor,
} from "./ReleasePreferences";

const fields = descriptors.filter(
  (d) => d.kind === "ruleField",
) as unknown as RuleFieldDescriptor[];

describe("registry-driven rule fields and Prefer rule scores", () => {
  it("serves every built-in field and no built-in ranking steps from the Go fixture", () => {
    expect(descriptors.filter((d) => d.kind === "rankingStep")).toEqual([]);
    expect(fields.map((f) => f.id)).toEqual([
      "language",
      "subtitles",
      "audio_matches_original",
      "name",
      "group",
      "source",
      "video",
      "audio",
      "height",
      "size_gib",
      "hdr",
      "edition",
      "indexer",
      "protocol",
    ]);
  });

  it("scores a new Prefer rule above the profile's other formats and drops stale rule scores", () => {
    const scores = {
      "ether:webdl": 4000,
      "ether:viewer-language": 20000,
      "ether:audio-transcode": -15,
    };
    expect(defaultPreferScore(scores)).toBe(50000);
    const rules = [
      { id: "flux", name: "FLUX", action: "prefer" as const, conditions: [] },
      { id: "cam", name: "No CAM", action: "avoid" as const, conditions: [] },
    ];
    const next = withRuleScores(rules, { ...scores, "rule:gone": 7, "rule:cam": 3 });
    expect(next["rule:flux"]).toBe(50000);
    expect(next["rule:gone"]).toBeUndefined();
    expect(next["rule:cam"]).toBeUndefined();
    expect(next["ether:webdl"]).toBe(4000);
    // An existing score is the owner's and is kept.
    expect(withRuleScores(rules, { "rule:flux": 12 })["rule:flux"]).toBe(12);
  });
  it("creates conditions with each field's declared operator and resolves manifest metadata", () => {
    expect(newCondition(fields, "name").operator).toBe("contains");
    expect(newCondition(fields, "group").operator).toBe("equals");
    expect(newCondition(fields, "height").operator).toBe("range");
    expect(newCondition(fields, "language").operator).toBe("equals");
    const size = fields.find((f) => f.id === "size_gib")!;
    expect(size.meta?.unit).toBe("GiB");
    expect(size.meta?.valueClass).toBe("number");

    const pluginField: RuleFieldDescriptor = {
      kind: "ruleField",
      id: "plugin:example.dub/dubbed",
      owner: "example.dub",
      group: "Dub language",
      label: { plugin: "example.dub", key: "field.dubbed", resolved: "Dubbed" },
      order: 100,
      meta: {
        valueClass: "" as unknown as "choice",
        operators: ["equals"],
        editor: {
          kind: "choice",
          options: [{ value: "ja", label: { resolved: "Japanese" } }],
        },
      },
    };
    const resolved = resolveRuleFieldMeta(pluginField.meta);
    expect(resolved?.defaultOperator).toBe("equals");
    expect(resolved?.valueClass).toBe("choice");
    expect(resolved?.choices).toEqual([{ value: "ja", label: { resolved: "Japanese" } }]);
    expect(descriptorOwnerName(pluginField)).toBe("Dub language");
    expect(descriptorLabel(pluginField)).toBe("Dubbed · From Dub language");
  });
});
