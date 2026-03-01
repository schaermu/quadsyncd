import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";

export default defineConfig({
  plugins: [svelte({ hot: !process.env.VITEST })],
  resolve: {
    conditions: ["browser"],
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["src/test/setup.ts"],
    include: ["src/**/*.test.ts", "src/**/*.test.svelte.ts"],
      coverage: {
      provider: "v8",
      include: ["src/**/*.{ts,svelte}"],
      exclude: [
        "src/main.ts",
        "src/app.css",
        "src/assets/**",
        "src/test/**",
        "src/pages/**",
        "src/App.svelte",
        // Not yet covered – SSE requires EventSource which is complex to mock
        "src/lib/sse.ts",
        // Not yet covered – complex table/alert components
        "src/components/ConflictAlert.svelte",
        "src/components/PlanOpsTable.svelte",
      ],
      reporter: ["text", "json-summary", "json", "html"],
      thresholds: {
        lines: 70,
        functions: 70,
        branches: 55,
        statements: 70,
      },
    },
  },
});
