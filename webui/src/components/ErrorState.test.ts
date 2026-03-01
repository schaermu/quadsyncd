import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/svelte";
import userEvent from "@testing-library/user-event";
import ErrorState from "./ErrorState.svelte";

describe("ErrorState", () => {
  it("renders the default message", () => {
    render(ErrorState);
    expect(screen.getByText("Something went wrong.")).toBeInTheDocument();
  });

  it("renders a custom message", () => {
    render(ErrorState, { props: { message: "Network error" } });
    expect(screen.getByText("Network error")).toBeInTheDocument();
  });

  it("does not render a Retry button when onretry is not provided", () => {
    render(ErrorState);
    expect(screen.queryByRole("button", { name: /retry/i })).not.toBeInTheDocument();
  });

  it("renders a Retry button when onretry is provided", () => {
    const onretry = vi.fn();
    render(ErrorState, { props: { onretry } });
    expect(screen.getByRole("button", { name: /retry/i })).toBeInTheDocument();
  });

  it("calls onretry when the Retry button is clicked", async () => {
    const user = userEvent.setup();
    const onretry = vi.fn();
    render(ErrorState, { props: { onretry } });
    await user.click(screen.getByRole("button", { name: /retry/i }));
    expect(onretry).toHaveBeenCalledTimes(1);
  });
});
