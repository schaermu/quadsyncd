/** Theme management: light/dark with localStorage persistence. */

const STORAGE_KEY = "quadsyncd-theme";
const LIGHT_THEME = "light";
const DARK_THEME = "dark";

type Theme = typeof LIGHT_THEME | typeof DARK_THEME;

function getSystemTheme(): Theme {
  if (
    typeof window !== "undefined" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
  ) {
    return DARK_THEME;
  }
  return LIGHT_THEME;
}

export function getStoredTheme(): Theme | null {
  if (typeof localStorage === "undefined") return null;
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === LIGHT_THEME || stored === DARK_THEME) {
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
    current === LIGHT_THEME ? DARK_THEME : LIGHT_THEME;
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

export { LIGHT_THEME, DARK_THEME };