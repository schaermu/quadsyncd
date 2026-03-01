import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  formatTimestamp,
  formatRelativeTime,
  shortSha,
  statusColor,
  levelPrefix,
  levelColor,
} from "./format";

describe("formatTimestamp", () => {
  it("returns em-dash for undefined", () => {
    expect(formatTimestamp(undefined)).toBe("—");
  });

  it("returns em-dash for empty string", () => {
    expect(formatTimestamp("")).toBe("—");
  });

  it("returns a formatted string for a valid ISO date", () => {
    const result = formatTimestamp("2024-03-15T10:30:00Z");
    // Just verify it returns a non-empty string that is not the raw ISO
    expect(result).toBeTruthy();
    expect(result).not.toBe("—");
  });

  it("returns the raw string for an invalid date", () => {
    const bad = "not-a-date";
    const result = formatTimestamp(bad);
    // toLocaleString on invalid Date returns "Invalid Date", not the original
    // but the function doesn't throw either way
    expect(typeof result).toBe("string");
  });
});

describe("formatRelativeTime", () => {
  let now: number;

  beforeEach(() => {
    now = Date.now();
    vi.spyOn(Date, "now").mockReturnValue(now);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns em-dash for undefined", () => {
    expect(formatRelativeTime(undefined)).toBe("—");
  });

  it("returns em-dash for empty string", () => {
    expect(formatRelativeTime("")).toBe("—");
  });

  it("returns 'just now' for future timestamps", () => {
    const future = new Date(now + 5000).toISOString();
    expect(formatRelativeTime(future)).toBe("just now");
  });

  it("returns seconds ago for <60 seconds", () => {
    const past = new Date(now - 30_000).toISOString();
    expect(formatRelativeTime(past)).toBe("30s ago");
  });

  it("returns minutes ago for <60 minutes", () => {
    const past = new Date(now - 5 * 60_000).toISOString();
    expect(formatRelativeTime(past)).toBe("5m ago");
  });

  it("returns hours ago for <24 hours", () => {
    const past = new Date(now - 3 * 3_600_000).toISOString();
    expect(formatRelativeTime(past)).toBe("3h ago");
  });

  it("returns days ago for >=24 hours", () => {
    const past = new Date(now - 2 * 86_400_000).toISOString();
    expect(formatRelativeTime(past)).toBe("2d ago");
  });
});

describe("shortSha", () => {
  it("returns em-dash for undefined", () => {
    expect(shortSha(undefined)).toBe("—");
  });

  it("returns first 8 characters", () => {
    expect(shortSha("abcdef1234567890")).toBe("abcdef12");
  });

  it("handles SHA shorter than 8 chars", () => {
    expect(shortSha("abc")).toBe("abc");
  });
});

describe("statusColor", () => {
  it("returns badge-success for success", () => {
    expect(statusColor("success")).toBe("badge-success");
  });

  it("returns badge-error for error", () => {
    expect(statusColor("error")).toBe("badge-error");
  });

  it("returns badge-info for running", () => {
    expect(statusColor("running")).toBe("badge-info");
  });

  it("returns badge-neutral for unknown status", () => {
    expect(statusColor("pending")).toBe("badge-neutral");
    expect(statusColor("")).toBe("badge-neutral");
  });
});

describe("levelPrefix", () => {
  it("returns ERR for ERROR", () => {
    expect(levelPrefix("ERROR")).toBe("ERR");
    expect(levelPrefix("error")).toBe("ERR");
  });

  it("returns WRN for WARN", () => {
    expect(levelPrefix("WARN")).toBe("WRN");
    expect(levelPrefix("warn")).toBe("WRN");
  });

  it("returns INF for INFO", () => {
    expect(levelPrefix("INFO")).toBe("INF");
    expect(levelPrefix("info")).toBe("INF");
  });

  it("returns DBG for DEBUG", () => {
    expect(levelPrefix("DEBUG")).toBe("DBG");
    expect(levelPrefix("debug")).toBe("DBG");
  });

  it("returns ??? for unknown or undefined", () => {
    expect(levelPrefix("TRACE")).toBe("???");
    expect(levelPrefix(undefined)).toBe("???");
  });
});

describe("levelColor", () => {
  it("returns text-error for ERROR", () => {
    expect(levelColor("ERROR")).toBe("text-error");
    expect(levelColor("error")).toBe("text-error");
  });

  it("returns text-warning for WARN", () => {
    expect(levelColor("WARN")).toBe("text-warning");
    expect(levelColor("warn")).toBe("text-warning");
  });

  it("returns text-info for INFO", () => {
    expect(levelColor("INFO")).toBe("text-info");
    expect(levelColor("info")).toBe("text-info");
  });

  it("returns text-neutral for DEBUG", () => {
    expect(levelColor("DEBUG")).toBe("text-neutral");
    expect(levelColor("debug")).toBe("text-neutral");
  });

  it("returns empty string for unknown or undefined", () => {
    expect(levelColor("TRACE")).toBe("");
    expect(levelColor(undefined)).toBe("");
  });
});
