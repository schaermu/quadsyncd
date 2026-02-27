/** Theme management: light/dark with localStorage persistence. */

const STORAGE_KEY = "quadsyncd-theme";
type Theme = "quadsyncd-light" | "quadsyncd-dark";

function getSystemTheme(): Theme {
  if (
    typeof window !== "undefined" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
  ) {
    return "quadsyncd-dark";
  }
  return "quadsyncd-light";
}

export function getStoredTheme(): Theme | null {
  if (typeof localStorage === "undefined") return null;
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === "quadsyncd-light" || stored === "quadsyncd-dark") {
    return stored;
  }
  return null;
}

export function getCurrentTheme(): Theme {
  return getStoredTheme() ?? getSystemTheme();
}

export function applyTheme(theme: Theme) {
  document.documentElement.setAttribute("data-theme", theme);
}

export function setTheme(theme: Theme) {
  localStorage.setItem(STORAGE_KEY, theme);
  applyTheme(theme);
}

export function toggleTheme(): Theme {
  const current = getCurrentTheme();
  const next: Theme =
    current === "quadsyncd-light" ? "quadsyncd-dark" : "quadsyncd-light";
  setTheme(next);
  return next;
}

export function initTheme() {
  applyTheme(getCurrentTheme());

  // Listen for system theme changes when no user preference
  if (typeof window !== "undefined") {
    window
      .matchMedia("(prefers-color-scheme: dark)")
      .addEventListener("change", () => {
        if (!getStoredTheme()) {
          applyTheme(getSystemTheme());
        }
      });
  }
}
