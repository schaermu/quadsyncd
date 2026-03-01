import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  getStoredTheme,
  getCurrentTheme,
  applyTheme,
  setTheme,
  toggleTheme,
} from "./theme";

describe("theme", () => {
  beforeEach(() => {
    // Clear localStorage before each test
    localStorage.clear();
    // Reset data-theme attribute
    document.documentElement.removeAttribute("data-theme");
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("getStoredTheme", () => {
    it("returns null when no theme is stored", () => {
      expect(getStoredTheme()).toBeNull();
    });

    it("returns stored light theme", () => {
      localStorage.setItem("quadsyncd-theme", "quadsyncd-light");
      expect(getStoredTheme()).toBe("quadsyncd-light");
    });

    it("returns stored dark theme", () => {
      localStorage.setItem("quadsyncd-theme", "quadsyncd-dark");
      expect(getStoredTheme()).toBe("quadsyncd-dark");
    });

    it("returns null for an unrecognized stored value", () => {
      localStorage.setItem("quadsyncd-theme", "some-other-theme");
      expect(getStoredTheme()).toBeNull();
    });
  });

  describe("getCurrentTheme", () => {
    it("returns stored theme when one is set", () => {
      localStorage.setItem("quadsyncd-theme", "quadsyncd-dark");
      expect(getCurrentTheme()).toBe("quadsyncd-dark");
    });

    it("falls back to system theme when nothing is stored", () => {
      // jsdom defaults to no matchMedia, so getSystemTheme returns quadsyncd-light
      const theme = getCurrentTheme();
      expect(["quadsyncd-light", "quadsyncd-dark"]).toContain(theme);
    });
  });

  describe("applyTheme", () => {
    it("sets the data-theme attribute on documentElement", () => {
      applyTheme("quadsyncd-dark");
      expect(document.documentElement.getAttribute("data-theme")).toBe("quadsyncd-dark");
    });

    it("updates data-theme when called again with a different theme", () => {
      applyTheme("quadsyncd-light");
      applyTheme("quadsyncd-dark");
      expect(document.documentElement.getAttribute("data-theme")).toBe("quadsyncd-dark");
    });
  });

  describe("setTheme", () => {
    it("persists theme to localStorage", () => {
      setTheme("quadsyncd-dark");
      expect(localStorage.getItem("quadsyncd-theme")).toBe("quadsyncd-dark");
    });

    it("applies the theme to the document", () => {
      setTheme("quadsyncd-light");
      expect(document.documentElement.getAttribute("data-theme")).toBe("quadsyncd-light");
    });
  });

  describe("toggleTheme", () => {
    it("toggles from light to dark", () => {
      localStorage.setItem("quadsyncd-theme", "quadsyncd-light");
      const next = toggleTheme();
      expect(next).toBe("quadsyncd-dark");
      expect(localStorage.getItem("quadsyncd-theme")).toBe("quadsyncd-dark");
    });

    it("toggles from dark to light", () => {
      localStorage.setItem("quadsyncd-theme", "quadsyncd-dark");
      const next = toggleTheme();
      expect(next).toBe("quadsyncd-light");
      expect(localStorage.getItem("quadsyncd-theme")).toBe("quadsyncd-light");
    });
  });
});
