import { SettingsRow as AppKitSettingsRow, cn } from "@braedonsaunders/appkit-ui";
import type { ComponentProps } from "react";
import { SettingAnchor } from "./SettingFocus";

/**
 * Keep labels readable beside wide controls by stacking the row on phones.
 * `setting` is the stable id a ⌘K jump uses to find this row.
 */
export function SettingsRow({
  className,
  stacked,
  setting,
  ...props
}: ComponentProps<typeof AppKitSettingsRow> & { setting?: string }) {
  const row = (
    <AppKitSettingsRow
      {...props}
      stacked={stacked}
      className={cn(!stacked && "max-sm:flex-col max-sm:items-stretch", className)}
    />
  );
  return setting ? <SettingAnchor id={setting}>{row}</SettingAnchor> : row;
}
