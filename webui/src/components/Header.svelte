<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { toggleTheme, getCurrentTheme } from "../lib/theme";
  import {
    connectSSE,
    disconnectSSE,
    getConnectionState,
    onConnectionStateChange,
  } from "../lib/sse";
  import { link, location } from "svelte-spa-router";

  let sseState = $state(getConnectionState());
  let theme = $state(getCurrentTheme());

  let cleanup: (() => void) | undefined;

  onMount(() => {
    connectSSE();
    cleanup = onConnectionStateChange((s) => {
      sseState = s;
    });
  });

  onDestroy(() => {
    cleanup?.();
    disconnectSSE();
  });

  function handleToggleTheme() {
    theme = toggleTheme();
  }

  function isActive(path: string): boolean {
    if (path === "/") return $location === "/";
    return $location.startsWith(path);
  }

  const sseIndicator = $derived.by(() => {
    switch (sseState) {
      case "connected":
        return { class: "bg-success", label: "Live" };
      case "connecting":
        return { class: "bg-warning", label: "Connecting" };
      default:
        return { class: "bg-error", label: "Offline" };
    }
  });
</script>

<header class="navbar bg-base-200 sticky top-0 z-50 shadow-sm">
  <div class="navbar-start">
    <a href="/" use:link class="btn btn-ghost text-lg font-bold">quadsyncd</a>
  </div>
  <div class="navbar-center hidden sm:flex">
    <nav class="flex gap-1">
      <a href="/" use:link class="btn btn-ghost btn-sm {isActive('/') ? 'btn-active' : ''}">Dashboard</a>
      <a href="/runs" use:link class="btn btn-ghost btn-sm {isActive('/runs') ? 'btn-active' : ''}">Runs</a>
      <a href="/plan" use:link class="btn btn-ghost btn-sm {isActive('/plan') ? 'btn-active' : ''}">Plan</a>
      <a href="/units" use:link class="btn btn-ghost btn-sm {isActive('/units') ? 'btn-active' : ''}">Units</a>
    </nav>
  </div>
  <div class="navbar-end gap-2">
    <div
      class="flex items-center gap-1.5 text-xs px-2 py-1 rounded-full bg-base-300"
      role="status"
      aria-live="polite"
      title="SSE connection: {sseState}"
    >
      <span class="inline-block w-2 h-2 rounded-full {sseIndicator.class}"
      ></span>
      <span class="hidden sm:inline">{sseIndicator.label}</span>
    </div>
    <button
      class="btn btn-ghost btn-sm btn-circle"
      onclick={handleToggleTheme}
      aria-label="Toggle theme"
    >
      {#if theme === "quadsyncd-dark"}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-5 w-5"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          ><path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z"
          /></svg
        >
      {:else}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-5 w-5"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          ><path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z"
          /></svg
        >
      {/if}
    </button>
    <!-- Mobile nav dropdown -->
    <div class="dropdown dropdown-end sm:hidden">
      <div tabindex="0" role="button" class="btn btn-ghost btn-sm btn-circle" aria-label="Open navigation menu">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-5 w-5"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M4 6h16M4 12h16M4 18h16"
          />
        </svg>
      </div>
      <ul
        tabindex="0"
        class="menu menu-sm dropdown-content bg-base-200 rounded-box z-10 mt-3 w-40 p-2 shadow"
      >
        <li><a href="/" use:link class="{isActive('/') ? 'active' : ''}">Dashboard</a></li>
        <li><a href="/runs" use:link class="{isActive('/runs') ? 'active' : ''}">Runs</a></li>
        <li><a href="/plan" use:link class="{isActive('/plan') ? 'active' : ''}">Plan</a></li>
        <li><a href="/units" use:link class="{isActive('/units') ? 'active' : ''}">Units</a></li>
      </ul>
    </div>
  </div>
</header>
