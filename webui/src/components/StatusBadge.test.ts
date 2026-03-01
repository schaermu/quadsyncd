import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import StatusBadge from "./StatusBadge.svelte";

describe("StatusBadge", () => {
  it("renders the status text", () => {
    render(StatusBadge, { props: { status: "success" } });
    expect(screen.getByText("success")).toBeInTheDocument();
  });

  it("applies badge-success class for success status", () => {
    render(StatusBadge, { props: { status: "success" } });
    const badge = screen.getByText("success");
    expect(badge.className).toContain("badge-success");
  });

  it("applies badge-error class for error status", () => {
    render(StatusBadge, { props: { status: "error" } });
    const badge = screen.getByText("error");
    expect(badge.className).toContain("badge-error");
  });

  it("applies badge-info class for running status", () => {
    render(StatusBadge, { props: { status: "running" } });
    const badge = screen.getByText("running");
    expect(badge.className).toContain("badge-info");
  });

  it("applies badge-neutral class for unknown status", () => {
    render(StatusBadge, { props: { status: "pending" } });
    const badge = screen.getByText("pending");
    expect(badge.className).toContain("badge-neutral");
  });
});
