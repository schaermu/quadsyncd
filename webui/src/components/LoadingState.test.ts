import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import LoadingState from "./LoadingState.svelte";

describe("LoadingState", () => {
  it("renders the default message", () => {
    render(LoadingState);
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });

  it("renders a custom message", () => {
    render(LoadingState, { props: { message: "Fetching data…" } });
    expect(screen.getByText("Fetching data…")).toBeInTheDocument();
  });

  it("has role=status for accessibility", () => {
    render(LoadingState);
    expect(screen.getByRole("status")).toBeInTheDocument();
  });
});
