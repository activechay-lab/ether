import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { NumberInput } from "../components/fields";

function Harness({ initial, min }: { initial: number; min?: number }) {
  const [value, setValue] = useState(initial);
  return <NumberInput aria-label="Amount" value={value} onChange={setValue} min={min} />;
}

describe("NumberInput", () => {
  it("can be cleared with backspace instead of snapping back to 0", () => {
    render(<Harness initial={4} />);
    const input = screen.getByRole("spinbutton", { name: "Amount" });
    fireEvent.change(input, { target: { value: "" } });
    expect(input).toHaveValue(null);
  });

  it("restores the last committed value on blur if left empty", () => {
    render(<Harness initial={4} />);
    const input = screen.getByRole("spinbutton", { name: "Amount" });
    fireEvent.change(input, { target: { value: "" } });
    fireEvent.blur(input);
    expect(input).toHaveValue(4);
  });

  it("still commits every valid keystroke as before", () => {
    render(<Harness initial={4} min={0} />);
    const input = screen.getByRole("spinbutton", { name: "Amount" });
    fireEvent.change(input, { target: { value: "7" } });
    expect(input).toHaveValue(7);
  });
});
