import { useState } from "react";
import { ConnectionStatusRow, AddressChip } from "../../components/AppStatusList";
import { t } from "../../i18n";
import { formatDateTime } from "../../lib/format";
import {
  chosenAddress,
  homeSummary,
  usableAddresses,
  type ReachabilityStatus,
} from "./reachability";

/** The At home line names the address the owner is using, and keeps the rest behind a disclosure. */
export function HomeStatusRow({
  status,
  address,
}: {
  status: ReachabilityStatus | null | undefined;
  address?: string;
}) {
  const [showOther, setShowOther] = useState(false);
  const chosen = chosenAddress(status, address);
  const others = usableAddresses(status).filter((item) => item !== chosen);
  const home = homeSummary(status, chosen);
  const reason =
    status?.lanReason === "verified"
      ? t("Local identity verified")
      : status?.lanReason === "wrong_identity"
        ? t("Different server identity")
        : status?.lanReason === "probe_failed"
          ? t("Local connection failed")
          : status?.lanReason === "container"
            ? t("Can't check from inside a container")
            : status?.lanReason
              ? t("Local check unavailable")
              : undefined;
  const checked = status?.lanCheckedAt ? new Date(status.lanCheckedAt) : null;
  const detail =
    reason && checked && !Number.isNaN(checked.valueOf())
      ? t("{reason} · {time}. Checked from this server; other devices may differ.", {
          reason,
          time: formatDateTime(checked),
        })
      : home.detail;
  return (
    <div>
      <ConnectionStatusRow
        state={status?.lan}
        title={home.title}
        detail={detail}
        address={chosen}
      />
      {others.length > 0 ? (
        <div className="px-5 pb-3">
          <button
            type="button"
            className="text-sm text-primary"
            aria-expanded={showOther}
            onClick={() => setShowOther((v) => !v)}
          >
            {t("Other addresses")}
          </button>
          {showOther ? (
            <div className="mt-2 flex flex-wrap gap-2">
              {others.map((item) => (
                <AddressChip key={item} address={item} />
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
