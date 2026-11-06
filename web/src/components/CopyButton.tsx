import { Button, toast } from "@braedonsaunders/appkit-ui";
import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { t } from "../i18n";

/** Resolves false for denied or unavailable clipboard access, including HTTP. */
export async function copyText(value: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
    } else {
      const active = document.activeElement as HTMLElement | null;
      const input = document.createElement("textarea");
      input.value = value;
      input.style.position = "fixed";
      input.style.opacity = "0";
      document.body.appendChild(input);
      try {
        input.select();
        if (!document.execCommand("copy")) throw new Error("clipboard unavailable");
      } finally {
        input.remove();
        active?.focus();
      }
    }
    toast.success(t("Copied"));
    return true;
  } catch {
    toast.error(t("Copy failed"), { description: t("Copy the text by hand instead.") });
    return false;
  }
}

export function useCopy() {
  const [copied, setCopied] = useState(false);
  const copy = async (value: string) => {
    const ok = await copyText(value);
    setCopied(ok);
    return ok;
  };
  return { copied, copy };
}

export function CopyButton({ value, label }: { value: string; label: string }) {
  const { copied, copy } = useCopy();
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={() => void copy(value)}
      aria-label={label}
    >
      {copied ? <Check size={14} /> : <Copy size={14} />}
      {copied ? t("Copied") : t("Copy")}
    </Button>
  );
}
