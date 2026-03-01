import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import EmptyState from "./EmptyState.svelte";

describe("EmptyState", () => {
  it("renders the default message", () => {
    render(EmptyState);
    expect(screen.getByText("Nothing here yet.")).toBeInTheDocument();
  });

  it("renders a custom message", () => {
    render(EmptyState, { props: { message: "No runs found." } });
    expect(screen.getByText("No runs found.")).toBeInTheDocument();
  });

  it("renders the empty symbol", () => {
    const { container } = render(EmptyState);
    expect(container.textContent).toContain("∅");
  });
});
