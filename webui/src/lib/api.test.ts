import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  fetchOverview,
  fetchRuns,
  fetchRunDetail,
  fetchRunLogs,
  fetchRunPlan,
  fetchUnits,
  fetchTimer,
  triggerPlan,
} from "./api";

// Helper to build a minimal Response-like object
function mockResponse(body: unknown, ok = true, status = 200): Response {
  return {
    ok,
    status,
    text: () => Promise.resolve(typeof body === "string" ? body : JSON.stringify(body)),
    json: () => Promise.resolve(body),
  } as unknown as Response;
}

describe("api client", () => {
  beforeEach(() => {
    // Reset cookies before each test
    Object.defineProperty(document, "cookie", {
      writable: true,
      value: "",
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("fetchOverview", () => {
    it("GETs /api/overview and returns parsed JSON", async () => {
      const payload = { repositories: [], last_run_id: "r1", last_run_status: "success" };
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse(payload));
      const result = await fetchOverview();
      expect(fetch).toHaveBeenCalledWith("/api/overview", undefined);
      expect(result).toEqual(payload);
    });

    it("throws on non-ok response", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse("server error", false, 500));
      await expect(fetchOverview()).rejects.toThrow("API 500");
    });
  });

  describe("fetchRuns", () => {
    it("GETs /api/runs with default limit", async () => {
      const payload = { items: [] };
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse(payload));
      await fetchRuns();
      expect(fetch).toHaveBeenCalledWith("/api/runs?limit=20", undefined);
    });

    it("includes cursor param when provided", async () => {
      const payload = { items: [] };
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse(payload));
      await fetchRuns(10, "cursor-abc");
      expect(fetch).toHaveBeenCalledWith("/api/runs?limit=10&cursor=cursor-abc", undefined);
    });
  });

  describe("fetchRunDetail", () => {
    it("GETs /api/runs/:id", async () => {
      const payload = { id: "run-1", status: "success" };
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse(payload));
      const result = await fetchRunDetail("run-1");
      expect(fetch).toHaveBeenCalledWith("/api/runs/run-1", { signal: undefined });
      expect(result).toEqual(payload);
    });

    it("URL-encodes the run id", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse({}));
      await fetchRunDetail("run/with/slashes");
      expect(fetch).toHaveBeenCalledWith("/api/runs/run%2Fwith%2Fslashes", expect.anything());
    });
  });

  describe("fetchRunLogs", () => {
    it("GETs /api/runs/:id/logs with no filters", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse({ items: [] }));
      await fetchRunLogs("run-1");
      expect(fetch).toHaveBeenCalledWith("/api/runs/run-1/logs?", { signal: undefined });
    });

    it("includes filter params in query string", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse({ items: [] }));
      await fetchRunLogs("run-1", { level: "error", q: "failed", limit: 50 });
      const url = (fetch as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
      expect(url).toContain("level=error");
      expect(url).toContain("q=failed");
      expect(url).toContain("limit=50");
    });
  });

  describe("fetchRunPlan", () => {
    it("GETs /api/runs/:id/plan", async () => {
      const payload = { requested: {}, conflicts: [], ops: [] };
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse(payload));
      const result = await fetchRunPlan("run-1");
      expect(fetch).toHaveBeenCalledWith("/api/runs/run-1/plan", { signal: undefined });
      expect(result).toEqual(payload);
    });
  });

  describe("fetchUnits", () => {
    it("GETs /api/units", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse({ items: [] }));
      await fetchUnits();
      expect(fetch).toHaveBeenCalledWith("/api/units", undefined);
    });
  });

  describe("fetchTimer", () => {
    it("GETs /api/timer", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse({ unit: "quadsyncd.timer", active: true }));
      await fetchTimer();
      expect(fetch).toHaveBeenCalledWith("/api/timer", undefined);
    });
  });

  describe("triggerPlan", () => {
    it("POSTs to /api/plan with CSRF token from cookie", async () => {
      Object.defineProperty(document, "cookie", {
        writable: true,
        value: "csrf_token=test-token-123",
      });
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse({ run_id: "new-run" }));
      const result = await triggerPlan();
      expect(fetch).toHaveBeenCalledWith(
        "/api/plan",
        expect.objectContaining({
          method: "POST",
          headers: expect.objectContaining({
            "X-CSRF-Token": "test-token-123",
          }),
        }),
      );
      expect(result).toEqual({ run_id: "new-run" });
    });

    it("sends empty CSRF token when cookie is absent", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(mockResponse({ run_id: "r2" }));
      await triggerPlan();
      expect(fetch).toHaveBeenCalledWith(
        "/api/plan",
        expect.objectContaining({
          headers: expect.objectContaining({ "X-CSRF-Token": "" }),
        }),
      );
    });
  });
});
