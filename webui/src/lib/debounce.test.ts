import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { debounce } from "./debounce";

describe("debounce", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("does not call fn immediately", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced();
    expect(fn).not.toHaveBeenCalled();
  });

  it("calls fn after the delay elapses", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced();
    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("passes arguments to fn", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced("a", "b");
    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledWith("a", "b");
  });

  it("resets the timer when called again before delay", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced();
    vi.advanceTimersByTime(50);
    debounced();
    vi.advanceTimersByTime(50);
    // first invocation timer was cancelled; second timer hasn't elapsed yet
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(50);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("uses the last arguments when debounced multiple times", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced("first");
    debounced("second");
    debounced("third");
    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith("third");
  });

  it("cancel() prevents pending invocation", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced();
    debounced.cancel();
    vi.advanceTimersByTime(200);
    expect(fn).not.toHaveBeenCalled();
  });

  it("cancel() is safe to call when no timer is pending", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    expect(() => debounced.cancel()).not.toThrow();
  });

  it("allows new invocations after cancel()", () => {
    const fn = vi.fn();
    const debounced = debounce(fn, 100);
    debounced();
    debounced.cancel();
    debounced("after-cancel");
    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith("after-cancel");
  });
});
