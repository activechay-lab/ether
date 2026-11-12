import { expect, it } from "vitest";
import { serverBaseFromTunnel, shortId, sidecarCommand } from "./SidecarPairing";

it("recovers the server base from a tunnel URL for wss, ws and bare hosts", () => {
  expect(serverBaseFromTunnel("wss://ether.example.test/ether/plugins/tunnel")).toBe(
    "https://ether.example.test",
  );
  expect(serverBaseFromTunnel("ws://10.0.0.5:32450/ether/plugins/tunnel")).toBe(
    "http://10.0.0.5:32450",
  );
  expect(serverBaseFromTunnel("https://ether.example.test")).toBe("https://ether.example.test");
});

it("sanitizes a plugin id into a shell-safe short id", () => {
  expect(shortId("community.homeassistant.notify")).toBe("notify");
  expect(shortId("org.ether.Example Plugin")).toBe("example-plugin");
  expect(shortId("...")).toBe("sidecar");
});

it("renders the docker command with the pairing code and server URL filled in", () => {
  const command = sidecarCommand(
    { docker: "ghcr.io/ether/home-assistant:1.2.3" },
    "community.homeassistant.notify",
    "PAIR-1234",
    "wss://ether.example.test/ether/plugins/tunnel",
  );
  expect(command).toEqual({
    kind: "docker",
    line:
      "docker run -d --name ether-notify --restart unless-stopped" +
      " -e ETHER_URL=https://ether.example.test" +
      " -e ETHER_PAIRING_CODE=PAIR-1234" +
      " -v ether-notify:/data" +
      " ghcr.io/ether/home-assistant:1.2.3",
  });
});

it("renders a download command when no docker image is given", () => {
  const command = sidecarCommand(
    { download: "https://example.com/sidecar", command: "python3 plugin.py" },
    "community.example.p",
    "PAIR-9",
    "ws://10.0.0.5:32450/ether/plugins/tunnel",
  );
  expect(command).toEqual({
    kind: "download",
    line: "ETHER_URL=http://10.0.0.5:32450 ETHER_PAIRING_CODE=PAIR-9 python3 plugin.py",
  });
});

it("has no ready command without docker or download", () => {
  expect(sidecarCommand(undefined, "p", "c", "wss://h/ether/plugins/tunnel")).toBeNull();
  expect(
    sidecarCommand({ docs: "https://example.com" }, "p", "c", "wss://h/ether/plugins/tunnel"),
  ).toBeNull();
});
