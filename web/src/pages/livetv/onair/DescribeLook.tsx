import { AgentPanel } from "@braedonsaunders/appkit-ai/react";
import { Alert, AlertDescription, Button, Badge } from "@braedonsaunders/appkit-ui";
import { Sparkles, Check } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { t } from "../../../i18n";
import type { Look } from "./model";
export function DescribeLook({ look, onChange }: { look: Look; onChange: (l: Look) => void }) {
  const [steps, setSteps] = useState<string[]>([]),
    [working, setWorking] = useState(false);
  const before = useRef<Look>(look);
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);
  useEffect(() => () => timers.current.forEach(clearTimeout), []);
  const draft = (prompt: string) => {
    before.current = structuredClone(look);
    setSteps([]);
    setWorking(true);
    let current = structuredClone(look);
    const steps = [
      t("Palette → midnight violet and warm gold"),
      t("Type → Fraunces / Inter"),
      t("Ident and programme cards updated"),
      t("Break and off-air moments ready"),
    ];
    steps.forEach((text, i) =>
      timers.current.push(
        setTimeout(
          () => {
            if (i === 0)
              current = {
                ...current,
                brand: {
                  ...current.brand,
                  palette: {
                    background: "#100d20",
                    accent: "#D8B26E",
                    text: "#F4EEDF",
                    glow: "#8b78bb",
                  },
                },
              };
            if (i === 1)
              current = {
                ...current,
                brand: { ...current.brand, type: { display: "Fraunces", body: "Inter" } },
              };
            if (i === 2)
              current = {
                ...current,
                brand: { ...current.brand, energy: 0.28, seed: current.brand.seed + 1 },
              };
            onChange(current);
            setSteps((s) => [...s, text]);
            if (i === 3) setWorking(false);
          },
          900 + i * 1500,
        ),
      ),
    );
    void prompt;
  };
  return (
    <div className="flex min-h-[570px] flex-col gap-4">
      <div className="flex items-center gap-2">
        <Sparkles size={16} className="text-primary" />
        <span className="text-sm font-medium">{t("Describe your look")}</span>
        {working ? <Badge>{t("Drafting…")}</Badge> : null}
      </div>
      {steps.length ? (
        <div className="space-y-2 rounded-lg border border-border bg-bg-subtle p-3">
          {steps.map((step, i) => (
            <button
              key={step}
              className="flex w-full items-center gap-2 text-left text-xs text-fg-muted hover:text-primary"
              title={t("Revert change")}
              onClick={() => {
                const original = before.current;
                onChange({
                  ...look,
                  brand: {
                    ...look.brand,
                    ...(i === 0
                      ? { palette: original.brand.palette }
                      : i === 1
                        ? { type: original.brand.type }
                        : { seed: original.brand.seed, energy: original.brand.energy }),
                  },
                });
                setSteps((s) => s.filter((x) => x !== step));
              }}
            >
              <Check size={13} className="text-success" />
              {step}
            </button>
          ))}
        </div>
      ) : null}
      {!import.meta.env.MODE.includes("mock") ? (
        <Alert>
          <AlertDescription>
            {t("Connect a language model to describe a look.")}{" "}
            <Link className="text-primary underline" to="/integrations/language-model">
              {t("Integrations › Language model")}
            </Link>
          </AlertDescription>
        </Alert>
      ) : null}
      <AgentPanel
        enabled={import.meta.env.MODE === "mock"}
        working={working}
        suggestions={[
          t("A late-night horror look for Nocturne"),
          t("Match the look to Meridian’s programming"),
          t("Make it feel like a 90s kids network"),
          t("Give it a calmer motion"),
        ]}
        labels={{
          title: t("Build with Ether"),
          welcomeTitle: t("What does your channel feel like?"),
          welcomeDescription: t("Describe the colours, mood and motion."),
          disabledTitle: t("Connect a model"),
        }}
        send={async (prompt) => {
          draft(prompt);
          return new Response(
            'data: {"type":"start"}\n\ndata: {"type":"text-start","id":"draft"}\n\ndata: {"type":"text-delta","id":"draft","delta":"' +
              t(
                "I’m building a draft. Watch the stage as the palette, type and moments come together.",
              ) +
              '"}\n\ndata: {"type":"text-end","id":"draft"}\n\ndata: {"type":"finish"}\n\ndata: [DONE]\n\n',
            {
              headers: {
                "content-type": "text/event-stream",
                "x-vercel-ai-ui-message-stream": "v1",
              },
            },
          );
        }}
      />
      <Button variant="ghost" size="sm" disabled={working} onClick={() => draft("calmer")}>
        {t("Try a calmer, cinematic direction")}
      </Button>
    </div>
  );
}
