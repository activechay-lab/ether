/**
 * Connect an AI app: MCP pairing inside a drawer nested over Ask Ether.
 * Shows connection instructions and the revocable list of paired apps.
 */
import { Button, Spinner, toast } from "@braedonsaunders/appkit-ui";
import { CopyButton } from "../CopyButton";
import { useCallback, useEffect, useState } from "react";
import { t } from "../../i18n";
import { formatDate } from "../../lib/format";
import { Drawer } from "../Drawer";
import { ErrorPanel } from "../feedback";
import { fetchMcpClients, revokeMcpClient, type McpClient } from "./agentApi";

export function McpPairingDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [clients, setClients] = useState<McpClient[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [revoking, setRevoking] = useState<string | null>(null);

  const loadClients = useCallback(async () => {
    setClients(await fetchMcpClients());
  }, []);

  useEffect(() => {
    if (!open) return;
    setError(null);
    void loadClients().catch((e: unknown) => setError(e));
  }, [open, loadClients]);

  const revoke = async (client: McpClient) => {
    setRevoking(client.id);
    setError(null);
    try {
      await revokeMcpClient(client.id);
      setClients((current) => (current ?? []).filter((item) => item.id !== client.id));
      toast.success(t("Access revoked"), {
        description: t("{name} can no longer connect to this server.", { name: client.name }),
      });
    } catch (e) {
      setError(e);
    } finally {
      setRevoking(null);
    }
  };

  const endpoint = `${window.location.origin}/ether/mcp`;
  const codeCommand = `claude mcp add --transport http ether ${endpoint}`;
  const desktopConfig = JSON.stringify(
    {
      mcpServers: {
        ether: { command: "npx", args: ["-y", "mcp-remote", endpoint, "--allow-http"] },
      },
    },
    null,
    2,
  );

  return (
    <Drawer open={open} onClose={onClose} title={t("Connect an AI app")} size="sm">
      <div className="space-y-6">
        <p className="text-sm leading-relaxed text-fg-muted">
          {t(
            "Pair an AI app so it can ask Ether questions with the same tools as Ask Ether. Anything that changes your server still needs your approval here.",
          )}
        </p>

        {error ? <ErrorPanel error={error} /> : null}

        <section aria-label={t("Connect an app")} className="space-y-3">
          <h3 className="text-sm font-semibold text-fg">Claude Code</h3>
          <p className="text-sm text-fg-muted">
            {t("Run this command, then approve the request in Ether.")}
          </p>
          <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-lg bg-bg-subtle p-3 text-xs">
            {codeCommand}
          </pre>
          <CopyButton value={codeCommand} label={t("Copy Claude Code command")} />
          <h3 className="text-sm font-semibold text-fg">Claude Desktop</h3>
          <p className="text-sm text-fg-muted">
            {t(
              "Install Node.js, add this server to your Claude Desktop configuration, then approve the request in Ether.",
            )}
          </p>
          <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-lg bg-bg-subtle p-3 text-xs">
            {desktopConfig}
          </pre>
          <CopyButton value={desktopConfig} label={t("Copy Claude Desktop configuration")} />
        </section>

        <section aria-label={t("Paired apps")} className="space-y-3">
          <h3 className="text-sm font-semibold text-fg">{t("Paired apps")}</h3>
          {clients === null ? (
            <div className="grid place-items-center p-4">
              <Spinner size={18} label={t("Loading paired apps")} />
            </div>
          ) : clients.length === 0 ? (
            <p className="text-sm text-fg-muted">{t("No AI apps are paired with this server.")}</p>
          ) : (
            <ul className="divide-y divide-border rounded-xl border border-border">
              {clients.map((client) => {
                const pairedOn = client.createdAt ? formatDate(client.createdAt) : "";
                return (
                  <li key={client.id} className="flex items-center gap-3 px-3 py-2.5">
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium text-fg">
                        {client.name}
                      </span>
                      {pairedOn ? (
                        <span className="block text-xs text-fg-muted">
                          {t("Paired on {date}", { date: pairedOn })}
                        </span>
                      ) : null}
                    </span>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={revoking === client.id}
                      onClick={() => void revoke(client)}
                    >
                      {t("Revoke")}
                    </Button>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      </div>
    </Drawer>
  );
}
