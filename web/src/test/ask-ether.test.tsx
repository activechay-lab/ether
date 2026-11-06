import { act, fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setMockAgentEnabled } from "../mock/extend/agent";
import { calls, renderApp, settleUi } from "./harness";

afterEach(() => {
  setMockAgentEnabled(true);
});

async function openAskEther() {
  fireEvent.click(await screen.findByRole("button", { name: "Ask Ether" }));
  return screen.findByRole("dialog");
}

async function expectDrawerClosed() {
  await settleUi(() => {
    expect(screen.queryByRole("dialog")).toBeNull();
  });
}

/** The streamed row is remounted when the durable transcript is adopted. */
async function expectAttentionAnswer(dialog: HTMLElement) {
  await settleUi(() => {
    expect(within(dialog).getByText(/Nothing needs urgent attention/)).toBeInTheDocument();
  });
}

describe("Ask Ether", () => {
  it("opens from the top bar and closes with Escape", async () => {
    renderApp("/");
    const dialog = await openAskEther();
    expect(within(dialog).getAllByText("Ask Ether").length).toBeGreaterThan(0);
    expect(await within(dialog).findByPlaceholderText("Ask Ether…")).toBeInTheDocument();
    fireEvent.keyDown(document, { key: "Escape" });
    await expectDrawerClosed();
  });

  it("toggles with the keyboard shortcut", async () => {
    const user = userEvent.setup();
    renderApp("/");
    await screen.findByRole("button", { name: "Ask Ether" });
    await user.keyboard("{Meta>}j{/Meta}");
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(await screen.findByPlaceholderText("Ask Ether…")).toBeInTheDocument();
    await user.keyboard("{Control>}j{/Control}");
    await expectDrawerClosed();
  });

  it("sends a prompt and streams the answer with its tool step", async () => {
    const user = userEvent.setup();
    const { fake } = renderApp("/");
    const dialog = await openAskEther();
    const composer = await within(dialog).findByPlaceholderText("Ask Ether…");
    await user.type(composer, "What needs my attention?");
    await user.click(within(dialog).getByRole("button", { name: "Send" }));

    await expectAttentionAnswer(dialog);
    expect(within(dialog).getByText(/Server status/)).toBeInTheDocument();

    const turns = calls(fake, "POST", "/agent/turn");
    expect(turns).toHaveLength(1);
    expect(turns[0]!.body).toMatchObject({
      prompt: "What needs my attention?",
      context: { route: "/" },
    });
    // The durable transcript is refetched once the stream finishes.
    await settleUi(() => {
      expect(calls(fake, "GET", "/agent/threads/").length).toBeGreaterThan(0);
    });
    // The streamed answer is adopted once, not duplicated by the refetch.
    await settleUi(() => {
      expect(within(dialog).getAllByText(/Nothing needs urgent attention/)).toHaveLength(1);
    });
  });

  it("shows an approval card after the turn and records the decision", async () => {
    const user = userEvent.setup();
    const { fake } = renderApp("/quality");
    const dialog = await openAskEther();
    const composer = await within(dialog).findByPlaceholderText("Ask Ether…");
    await user.type(composer, "Blocklist this release");
    await user.click(within(dialog).getByRole("button", { name: "Send" }));

    expect(await within(dialog).findByText("Approval needed")).toBeInTheDocument();
    expect(within(dialog).getByText("Blocklist a release")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Approve" }));

    expect(await within(dialog).findByText(/That release is blocklisted/)).toBeInTheDocument();
    const decisions = calls(fake, "POST", "/agent/approvals/");
    expect(decisions).toHaveLength(1);
    expect(decisions[0]!.body).toMatchObject({ decision: "approved" });
    expect(within(dialog).queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("stores a secret without ever echoing it", async () => {
    const user = userEvent.setup();
    const secretValue = "mock-secret-value-12345";
    // Hold the opening autofocus until credential entry has begun, regardless
    // of machine load. Other timers, including user-event's, run normally.
    let focusComposer: (() => void) | undefined;
    const schedule = vi.fn(window.setTimeout);
    vi.spyOn(window, "setTimeout").mockImplementation((callback, delay, ...args) => {
      if (delay === 80 && typeof callback === "function") {
        focusComposer = () => callback(...args);
        return schedule(() => {}, 0);
      }
      return schedule(callback, delay, ...args);
    });
    renderApp("/");
    const dialog = await openAskEther();
    const composer = await within(dialog).findByPlaceholderText("Ask Ether…");
    await user.type(composer, "Save my debrid API token");
    await user.click(within(dialog).getByRole("button", { name: "Send" }));

    expect(await within(dialog).findByText("Real-Debrid")).toBeInTheDocument();
    const field = await within(dialog).findByLabelText("API token");
    expect(field).toHaveAttribute("type", "password");
    await user.type(field, secretValue[0]!);
    expect(focusComposer).toBeTypeOf("function");
    act(() => focusComposer!());
    expect(field).toHaveFocus();
    await user.keyboard(secretValue.slice(1));
    expect(field).toHaveValue(secretValue);
    expect(composer).toHaveValue("");
    expect(document.body.textContent).not.toContain(secretValue);
    await user.click(within(dialog).getByRole("button", { name: "Save securely" }));

    await settleUi(() => {
      expect(within(dialog).getByText("Credential saved securely.")).toBeInTheDocument();
    });
    expect(document.body.textContent).not.toContain(secretValue);

    const threads = (await (await fetch("/ether/api/agent/threads")).json()) as {
      threads: { id: string }[];
    };
    const id = threads.threads[0]!.id;
    const transcript = await (await fetch(`/ether/api/agent/threads/${id}/messages`)).text();
    expect(transcript).not.toContain(secretValue);
    expect(transcript).toContain('"status":"stored"');
  });

  it("shows one connect card and nothing else when no model is configured", async () => {
    setMockAgentEnabled(false);
    renderApp("/");
    const dialog = await openAskEther();
    expect(await within(dialog).findByText("Connect a model")).toBeInTheDocument();
    const link = within(dialog).getByRole("link", {
      name: "Open Language model settings",
    });
    expect(link).toHaveAttribute("href", "/services/llm");
    expect(within(dialog).queryByPlaceholderText("Ask Ether…")).toBeNull();
    expect(within(dialog).queryByRole("button", { name: "Send" })).toBeNull();
  });

  it.each([
    ["/", "What needs my attention?"],
    ["/quality", "Why did this title pick this release?"],
    ["/livetv", "Which channels are failing?"],
    ["/sources", "Add a plugin from Stremio"],
  ])("suggests a page prompt on %s", async (path, suggestion) => {
    renderApp(path);
    const dialog = await openAskEther();
    expect(await within(dialog).findByRole("button", { name: suggestion })).toBeInTheDocument();
  });

  it("does not show another page's suggestion", async () => {
    renderApp("/livetv");
    const dialog = await openAskEther();
    expect(
      await within(dialog).findByRole("button", { name: "Which channels are failing?" }),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("button", {
        name: "Why did this title pick this release?",
      }),
    ).toBeNull();
  });

  it("pairs an AI app and revokes a paired one", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    const { fake } = renderApp("/");
    const dialog = await openAskEther();
    await within(dialog).findByPlaceholderText("Ask Ether…");
    await user.click(within(dialog).getByRole("button", { name: "Conversation options" }));
    await user.click(await screen.findByText("Connect an AI app"));

    const pairing = await screen.findByRole("dialog", { name: "Connect an AI app" });
    const command = "claude mcp add --transport http ether http://localhost:3000/ether/mcp";
    expect(within(pairing).getByText(command)).toBeInTheDocument();
    expect(within(pairing).queryByText(/Bearer/)).toBeNull();
    await user.click(within(pairing).getByRole("button", { name: "Copy Claude Code command" }));
    expect(writeText).toHaveBeenCalledWith(command);

    expect(await within(pairing).findByText("Sample AI app")).toBeInTheDocument();
    await user.click(within(pairing).getByRole("button", { name: "Revoke" }));
    await settleUi(() => {
      expect(within(pairing).queryByText("Sample AI app")).toBeNull();
    });
    expect(calls(fake, "DELETE", "/agent/mcp/clients/")).toHaveLength(1);
  });

  it("lists earlier conversations and reopens one", async () => {
    const user = userEvent.setup();
    renderApp("/");
    const dialog = await openAskEther();
    const composer = await within(dialog).findByPlaceholderText("Ask Ether…");
    await user.type(composer, "What needs my attention?");
    await user.click(within(dialog).getByRole("button", { name: "Send" }));
    await expectAttentionAnswer(dialog);

    await user.click(within(dialog).getByRole("button", { name: "Conversation options" }));
    await user.click(await screen.findByText("New conversation"));
    await settleUi(() => {
      expect(within(dialog).queryByText(/Nothing needs urgent attention/)).toBeNull();
    });

    await user.click(within(dialog).getByRole("button", { name: "Conversation options" }));
    await user.click(await screen.findByText("Earlier conversations"));
    const history = await screen.findByRole("dialog", { name: "Earlier conversations" });
    await user.click(
      await within(history).findByRole("button", { name: /What needs my attention/ }),
    );
    await expectAttentionAnswer(dialog);
  });

  it("opens the route a navigate result carries", async () => {
    const user = userEvent.setup();
    renderApp("/");
    const dialog = await openAskEther();
    const composer = await within(dialog).findByPlaceholderText("Ask Ether…");
    await user.type(composer, "Go to the sources page");
    await user.click(within(dialog).getByRole("button", { name: "Send" }));
    await expectDrawerClosed();
    expect(
      await screen.findByRole("heading", { name: "Sources" }, { timeout: 10000 }),
    ).toBeInTheDocument();
  });

  it("clears the conversation from the header menu", async () => {
    const user = userEvent.setup();
    const { fake } = renderApp("/");
    const dialog = await openAskEther();
    const composer = await within(dialog).findByPlaceholderText("Ask Ether…");
    await user.type(composer, "What needs my attention?");
    await user.click(within(dialog).getByRole("button", { name: "Send" }));
    await expectAttentionAnswer(dialog);

    await user.click(within(dialog).getByRole("button", { name: "Conversation options" }));
    await user.click(await screen.findByText("Clear this conversation"));
    await settleUi(() => {
      expect(within(dialog).queryByText(/Nothing needs urgent attention/)).toBeNull();
      expect(calls(fake, "DELETE", "/agent/threads/")).toHaveLength(1);
    });
    expect(within(dialog).getByPlaceholderText("Ask Ether…")).toBeInTheDocument();
  });
});

describe("MCP authorization", () => {
  it.each([
    ["Approve", true],
    ["Deny", false],
  ] as const)("shows the app and sends %s", async (label, approve) => {
    const { fake } = renderApp("/mcp/authorize?request=example");
    expect(await screen.findByText("Claude wants to connect to Ether.")).toBeInTheDocument();
    expect(screen.getByText("Access: read")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
    expect(calls(fake, "GET", "/agent/mcp/authorization")).toHaveLength(1);
    await userEvent.setup().click(screen.getByRole("button", { name: label }));
    await settleUi(() => expect(calls(fake, "POST", "/agent/mcp/authorization")).toHaveLength(1));
    expect(calls(fake, "POST", "/agent/mcp/authorization")[0]?.body).toEqual({
      request: "example",
      approve,
    });
  });
});
