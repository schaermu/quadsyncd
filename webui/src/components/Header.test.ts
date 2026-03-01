import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/svelte";
import userEvent from "@testing-library/user-event";

// Mock svelte-spa-router before importing the component
vi.mock("svelte-spa-router", () => ({
  link: () => {},
  location: { subscribe: (fn: (v: string) => void) => { fn("/"); return () => {}; } },
}));

// Mock SSE module
vi.mock("../lib/sse", () => ({
  connectSSE: vi.fn(),
  disconnectSSE: vi.fn(),
  getConnectionState: vi.fn(() => "disconnected"),
  onConnectionStateChange: vi.fn(() => () => {}),
}));

// Mock theme module
vi.mock("../lib/theme", () => ({
  toggleTheme: vi.fn(() => "quadsyncd-dark"),
  getCurrentTheme: vi.fn(() => "quadsyncd-light"),
}));

import Header from "./Header.svelte";
import { connectSSE, disconnectSSE } from "../lib/sse";

describe("Header", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the brand link", () => {
    render(Header);
    expect(screen.getByText("quadsyncd")).toBeInTheDocument();
  });

  it("renders navigation links", () => {
    render(Header);
    expect(screen.getAllByText("Dashboard").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Runs").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Plan").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Units").length).toBeGreaterThan(0);
  });

  it("renders SSE connection status indicator", () => {
    render(Header);
    // The status indicator has role=status
    const statusEl = screen.getByRole("status");
    expect(statusEl).toBeInTheDocument();
  });

  it("renders theme toggle button", () => {
    render(Header);
    expect(screen.getByRole("button", { name: /toggle theme/i })).toBeInTheDocument();
  });

  it("connects SSE on mount", () => {
    render(Header);
    expect(connectSSE).toHaveBeenCalledTimes(1);
  });

  it("calls toggleTheme when the theme button is clicked", async () => {
    const { toggleTheme } = await import("../lib/theme");
    const user = userEvent.setup();
    render(Header);
    await user.click(screen.getByRole("button", { name: /toggle theme/i }));
    expect(toggleTheme).toHaveBeenCalledTimes(1);
  });
});
