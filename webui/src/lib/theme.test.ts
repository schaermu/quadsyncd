import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  getStoredTheme,
  getCurrentTheme,
  applyTheme,
  setTheme,
  toggleTheme,
  LIGHT_THEME,
  DARK_THEME,
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
      localStorage.setItem("quadsyncd-theme", LIGHT_THEME);
      expect(getStoredTheme()).toBe(LIGHT_THEME);
    });

    it("returns stored dark theme", () => {
      localStorage.setItem("quadsyncd-theme", DARK_THEME);
      expect(getStoredTheme()).toBe(DARK_THEME);
    });

    it("returns null for an unrecognized stored value", () => {
      localStorage.setItem("quadsyncd-theme", "some-other-theme");
      expect(getStoredTheme()).toBeNull();
    });
  });

  describe("getCurrentTheme", () => {
    it("returns stored theme when one is set", () => {
      localStorage.setItem("quadsyncd-theme", DARK_THEME);
      expect(getCurrentTheme()).toBe(DARK_THEME);
    });

    it("falls back to system theme when nothing is stored", () => {
      // jsdom defaults to no matchMedia, so getSystemTheme returns light
      const theme = getCurrentTheme();
      expect([LIGHT_THEME, DARK_THEME]).toContain(theme);
    });
  });

  describe("applyTheme", () => {
    it("sets the data-theme attribute on documentElement", () => {
      applyTheme(DARK_THEME);
      expect(document.documentElement.getAttribute("data-theme")).toBe(DARK_THEME);
    });

    it("updates data-theme when called again with a different theme", () => {
      applyTheme(LIGHT_THEME);
      applyTheme(DARK_THEME);
      expect(document.documentElement.getAttribute("data-theme")).toBe(DARK_THEME);
    });
  });

  describe("setTheme", () => {
    it("persists theme to localStorage", () => {
      setTheme(DARK_THEME);
      expect(localStorage.getItem("quadsyncd-theme")).toBe(DARK_THEME);
    });

    it("applies the theme to the document", () => {
      setTheme(LIGHT_THEME);
      expect(document.documentElement.getAttribute("data-theme")).toBe(LIGHT_THEME);
    });
  });

  describe("toggleTheme", () => {
    it("toggles from light to dark", () => {
      localStorage.setItem("quadsyncd-theme", LIGHT_THEME);
      const next = toggleTheme();
      expect(next).toBe(DARK_THEME);
      expect(localStorage.getItem("quadsyncd-theme")).toBe(DARK_THEME);
    });

    it("toggles from dark to light", () => {
      localStorage.setItem("quadsyncd-theme", DARK_THEME);
      const next = toggleTheme();
      expect(next).toBe(LIGHT_THEME);
      expect(localStorage.getItem("quadsyncd-theme")).toBe(LIGHT_THEME);
    });
  });
});
