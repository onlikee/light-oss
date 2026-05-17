import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import type { Bucket, Site } from "../../api/types";
import { renderWithApp } from "../../test/test-utils";
import { BucketList } from "./BucketList";

describe("BucketList", () => {
  beforeEach(() => {
    Object.defineProperty(Element.prototype, "hasPointerCapture", {
      configurable: true,
      value: vi.fn(() => false),
    });
    Object.defineProperty(Element.prototype, "setPointerCapture", {
      configurable: true,
      value: vi.fn(),
    });
    Object.defineProperty(Element.prototype, "releasePointerCapture", {
      configurable: true,
      value: vi.fn(),
    });
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
      configurable: true,
      value: vi.fn(),
    });
  });

  it("wraps long site values in the delete dialog", async () => {
    const rootPrefix =
      "nested/averyveryveryveryveryveryveryveryveryveryverylong-folder-prefix/";
    const domains = [
      "averyveryveryveryveryveryveryveryveryveryverylong-subdomain.localhost",
      "averyveryveryveryveryveryveryveryveryveryverylong-www.localhost",
    ];

    renderWithApp(
      <BucketList
        buckets={[createBucket()]}
        createPending={false}
        deleteDisabled={false}
        deletePendingBucket=""
        onCreateBucket={vi.fn().mockResolvedValue(undefined)}
        onDeleteBucket={vi.fn().mockResolvedValue(undefined)}
        onRefreshBuckets={vi.fn().mockResolvedValue(undefined)}
        onSearchInputChange={vi.fn()}
        onSearchSubmit={vi.fn()}
        search=""
        searchInput=""
        sitesByBucket={{
          demo: [createSite({ domains, root_prefix: rootPrefix })],
        }}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));

    const dialog = await screen.findByRole("alertdialog");
    const rootPrefixValue = within(dialog).getByText(rootPrefix);
    const domainsValue = within(dialog).getByText(domains.join(", "));

    expect(rootPrefixValue.className).toContain("wrap-anywhere");
    expect(domainsValue.className).toContain("wrap-anywhere");
  });
});

function createBucket(overrides: Partial<Bucket> = {}): Bucket {
  return {
    id: 1,
    name: "demo",
    created_at: "2026-04-07T01:00:00Z",
    updated_at: "2026-04-07T02:00:00Z",
    ...overrides,
  };
}

function createSite(overrides: Partial<Site> = {}): Site {
  return {
    id: 1,
    bucket: "demo",
    root_prefix: "app/",
    enabled: true,
    index_document: "index.html",
    error_document: "404.html",
    spa_fallback: false,
    domains: ["demo.localhost"],
    created_at: "2026-04-07T01:00:00Z",
    updated_at: "2026-04-07T02:00:00Z",
    ...overrides,
  };
}
