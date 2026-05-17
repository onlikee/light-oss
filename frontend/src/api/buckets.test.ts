import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AppSettings } from "../lib/settings";
import { listBuckets } from "./buckets";

const apiRequestMock = vi.fn();

vi.mock("./client", () => ({
  apiRequest: (...args: unknown[]) => apiRequestMock(...args),
}));

const settings: AppSettings = {
  apiBaseUrl: "https://api.localhost",
  bearerToken: "dev-token",
};

describe("buckets api helpers", () => {
  beforeEach(() => {
    apiRequestMock.mockReset();
    apiRequestMock.mockResolvedValue({ items: [] });
  });

  it("passes the search query when provided", async () => {
    await listBuckets(settings, { search: "  alp  " });

    expect(apiRequestMock).toHaveBeenCalledWith(
      settings,
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/buckets",
        params: {
          search: "alp",
        },
      }),
    );
  });

  it("omits query params when the search is empty", async () => {
    await listBuckets(settings, { search: "   " });

    expect(apiRequestMock).toHaveBeenCalledWith(
      settings,
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/buckets",
      }),
    );
    expect(apiRequestMock.mock.calls[0]?.[1]).not.toHaveProperty("params");
  });
});
