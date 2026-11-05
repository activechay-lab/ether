import { describe, expect, it } from "vitest";
import { sourceImportError } from "../pages/livetv/sourceErrors";
describe("source import errors", () => {
  it.each([
    [
      "playlist: source returned HTTP 503",
      "Couldn't download the playlist.",
      "503 (temporarily unavailable)",
    ],
    [
      "guide: lookup secret.example: no such host",
      "Couldn't download the guide.",
      "could not be found",
    ],
    [
      "playlist: dial tcp 10.2.3.4: connection refused",
      "Couldn't download the playlist.",
      "refused the connection",
    ],
    ["guide: x509 certificate error", "Couldn't download the guide.", "secure connection"],
    ["guide: context deadline exceeded", "Couldn't download the guide.", "took too long"],
    [
      "Xtream account: source returned HTTP 401",
      "Couldn't check the provider account.",
      "rejected the login",
    ],
    [
      "Xtream channels: source returned HTTP 429",
      "Couldn't download the channel list.",
      "too many requests",
    ],
  ])("explains %s without exposing transport details", (raw, step, cause) => {
    const message = sourceImportError(raw + " https://private.example/user/password?token=secret");
    expect(message).toContain(step);
    expect(message).toContain(cause);
    expect(message).toContain("Your previous import is still in use.");
    expect(message).not.toMatch(/private|password\?|secret|10\.2\.3\.4/);
  });
  it("does not claim a new source has an old import", () =>
    expect(sourceImportError("unknown private error", false)).toContain(
      "No channels were changed.",
    ));
});
