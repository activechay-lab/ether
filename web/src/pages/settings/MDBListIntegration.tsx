import { SettingsSection } from "@braedonsaunders/appkit-ui";
import type { ReactNode } from "react";
import { t } from "../../i18n";

/**
 * MDBList has no app credentials: each viewer pastes their own API key from
 * their Limits tab. The on/off switch is the Integrations overview card.
 */
export function MDBListSettings({ footer }: { footer?: ReactNode }) {
  return (
    <SettingsSection
      title={undefined}
      description={t(
        "Each viewer pastes their own MDBList API key from their Limits tab (MDBList Preferences → API).",
      )}
      footer={footer ?? undefined}
    >
      {null}
    </SettingsSection>
  );
}
