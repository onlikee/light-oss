import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { vi } from "vitest";
import { deleteBucket } from "@/api/buckets";
import { PreferencesProvider } from "@/lib/preferences";
import { SettingsProvider } from "@/lib/settings";
import { useDeleteBucket } from "./useDeleteBucket";

vi.mock("@/api/buckets", () => ({ deleteBucket: vi.fn() }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function setup() {
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  });
  const removePin = vi.fn();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <SettingsProvider
        initialSettings={{
          apiBaseUrl: "http://localhost:8080",
          bearerToken: "dev-token",
        }}
      >
        <PreferencesProvider
          initialPreferences={{ locale: "en-US", theme: "light" }}
        >
          {children}
        </PreferencesProvider>
      </SettingsProvider>
    </QueryClientProvider>
  );
  return {
    ...renderHook(() => useDeleteBucket(removePin), { wrapper }),
    client,
    removePin,
  };
}

const scope = ["http://localhost:8080", "dev-token"];

describe("useDeleteBucket", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("removes only the deleted bucket's explorer cache and invalidates all matching list searches", async () => {
    vi.mocked(deleteBucket).mockResolvedValue(undefined);
    const { result, client, removePin } = setup();
    const allBuckets = ["buckets", ...scope, ""];
    const searchBuckets = ["buckets", ...scope, "alpha"];
    const otherService = ["buckets", "http://other:8080", "dev-token", ""];
    const sites = ["sites", ...scope];
    const deletedExplorer = ["explorer-entries", ...scope, "alpha", "docs/"];
    const keptExplorer = ["explorer-entries", ...scope, "beta"];
    for (const key of [
      allBuckets,
      searchBuckets,
      otherService,
      sites,
      deletedExplorer,
      keptExplorer,
    ]) {
      client.setQueryData(key, { items: [] });
    }

    await act(async () => {
      await result.current.deleteBucket({ name: "alpha", id: 1 });
    });
    expect(removePin).toHaveBeenCalledWith(1);
    expect(client.getQueryState(deletedExplorer)).toBeUndefined();
    expect(client.getQueryState(keptExplorer)).toBeDefined();
    for (const key of [allBuckets, searchBuckets, sites]) {
      expect(client.getQueryState(key)?.isInvalidated).toBe(true);
    }
    expect(client.getQueryState(otherService)?.isInvalidated).toBe(false);
  });

  it("reports the pending bucket until deletion completes", async () => {
    let finish!: () => void;
    vi.mocked(deleteBucket).mockReturnValue(
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
    );
    const { result } = setup();
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.deleteBucket({ name: "alpha", id: 1 });
    });
    await waitFor(() =>
      expect(result.current.deletingBucketName).toBe("alpha"),
    );
    await act(async () => {
      finish();
      await pending;
    });
    await waitFor(() => expect(result.current.deletingBucketName).toBe(""));
  });

  it("retains pins and cache on failure and allows the caller to keep its dialog open", async () => {
    vi.mocked(deleteBucket).mockRejectedValue(new Error("delete failed"));
    const { result, client, removePin } = setup();
    const explorer = ["explorer-entries", ...scope, "alpha"];
    client.setQueryData(explorer, { items: [] });
    await act(async () => {
      await expect(
        result.current.deleteBucket({ name: "alpha", id: 1 }),
      ).rejects.toThrow("delete failed");
    });
    expect(removePin).not.toHaveBeenCalled();
    expect(client.getQueryState(explorer)).toBeDefined();
    await waitFor(() => expect(result.current.deletingBucketName).toBe(""));
  });
});
