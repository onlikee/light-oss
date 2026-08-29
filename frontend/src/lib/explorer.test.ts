import { describe, expect, it } from "vitest";
import { explorerPageSizes, parseExplorerLimit } from "./explorer";

describe("explorer helpers", () => {
  it("only includes page sizes accepted by the explorer API", () => {
    expect(explorerPageSizes).toContain(10);
    expect(explorerPageSizes).toContain(20);
    expect(explorerPageSizes).toContain(200);
    expect(explorerPageSizes).not.toContain(1000);
  });

  it("accepts supported explorer page limits", () => {
    expect(parseExplorerLimit("10")).toBe(10);
    expect(parseExplorerLimit("20")).toBe(20);
    expect(parseExplorerLimit("200")).toBe(200);
  });

  it("defaults to 20 when the limit is unsupported", () => {
    expect(parseExplorerLimit("15")).toBe(20);
    expect(parseExplorerLimit("1000")).toBe(20);
    expect(parseExplorerLimit(null)).toBe(20);
  });
});
