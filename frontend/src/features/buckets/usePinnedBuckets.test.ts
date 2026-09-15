import { act, renderHook } from "@testing-library/react";
import { toast } from "sonner";
import { vi } from "vitest";
import { usePinnedBuckets } from "./usePinnedBuckets";

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));
vi.mock("@/lib/i18n", () => {
  const t = (key: string) => key;
  return { useI18n: () => ({ t }) };
});

const key = "light-oss-pinned-buckets:http://localhost:8080";

describe("usePinnedBuckets", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
  });

  afterEach(() => vi.restoreAllMocks());

  it("appends pins, removes them, and restores their order after remount", () => {
    const { result, unmount } = renderHook(() =>
      usePinnedBuckets("http://localhost:8080"),
    );
    act(() => {
      result.current.togglePin(2);
      result.current.togglePin(1);
    });
    expect(result.current.pinnedIds).toEqual([2, 1]);
    act(() => result.current.togglePin(2));
    act(() => result.current.togglePin(2));
    expect(result.current.pinnedIds).toEqual([1, 2]);
    expect(JSON.parse(localStorage.getItem(key)!)).toEqual([1, 2]);
    unmount();
    const restored = renderHook(() =>
      usePinnedBuckets("http://localhost:8080"),
    );
    expect(restored.result.current.pinnedIds).toEqual([1, 2]);
  });

  it("isolates services and normalizes whitespace and trailing slashes", () => {
    localStorage.setItem(key, "[2,1]");
    const { result, rerender } = renderHook(
      ({ url }) => usePinnedBuckets(url),
      {
        initialProps: { url: " http://localhost:8080/// " },
      },
    );
    expect(result.current.pinnedIds).toEqual([2, 1]);
    rerender({ url: "http://other:8080" });
    expect(result.current.pinnedIds).toEqual([]);
    act(() => result.current.togglePin(9));
    expect(localStorage.getItem(key)).toBe("[2,1]");
    rerender({ url: "http://localhost:8080/" });
    expect(result.current.pinnedIds).toEqual([2, 1]);
  });

  it("removes a completed deletion from its original service without losing newer pins", () => {
    const { result, rerender } = renderHook(
      ({ url }) => usePinnedBuckets(url),
      {
        initialProps: { url: "http://localhost:8080" },
      },
    );
    act(() => result.current.togglePin(1));
    const removeAfterDelete = result.current.removePin;
    act(() => result.current.togglePin(2));
    rerender({ url: "http://other:8080" });
    act(() => result.current.togglePin(9));
    act(() => removeAfterDelete(1));
    expect(result.current.pinnedIds).toEqual([9]);
    expect(localStorage.getItem(key)).toBe("[2]");
  });

  it.each(["broken", "null", "{}", '[1,"2"]', "[-1]", "[1.5]"])(
    "treats invalid stored data %s as empty",
    (value) => {
      localStorage.setItem(key, value);
      const { result } = renderHook(() =>
        usePinnedBuckets("http://localhost:8080"),
      );
      expect(result.current.pinnedIds).toEqual([]);
      act(() => result.current.togglePin(3));
      expect(localStorage.getItem(key)).toBe("[3]");
    },
  );

  it("deduplicates stored IDs while preserving order and reconciles deleted IDs", () => {
    localStorage.setItem(key, "[2,1,2,3]");
    const { result } = renderHook(() =>
      usePinnedBuckets("http://localhost:8080"),
    );
    expect(result.current.pinnedIds).toEqual([2, 1, 3]);
    act(() => result.current.reconcilePins([1, 2, 4]));
    expect(result.current.pinnedIds).toEqual([2, 1]);
    expect(localStorage.getItem(key)).toBe("[2,1]");
  });

  it("reports storage read failures", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    const { result } = renderHook(() =>
      usePinnedBuckets("http://localhost:8080"),
    );
    expect(result.current.pinnedIds).toEqual([]);
    expect(toast.error).toHaveBeenCalledWith("buckets.pin.storageError");
  });

  it("keeps the previous state when saving or removing a pin fails", () => {
    localStorage.setItem(key, "[1]");
    const { result } = renderHook(() =>
      usePinnedBuckets("http://localhost:8080"),
    );
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    act(() => result.current.togglePin(2));
    expect(result.current.pinnedIds).toEqual([1]);
    act(() => result.current.removePin(1));
    expect(result.current.pinnedIds).toEqual([1]);
    expect(localStorage.getItem(key)).toBe("[1]");
    expect(toast.error).toHaveBeenCalledTimes(2);
  });
});
