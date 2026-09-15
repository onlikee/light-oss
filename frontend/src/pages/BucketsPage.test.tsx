import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes, useLocation } from "react-router-dom";
import { vi } from "vitest";
import { BucketsPage } from "./BucketsPage";
import { renderWithApp } from "../test/test-utils";

vi.mock("../api/buckets", () => ({
  listBuckets: vi.fn(),
  createBucket: vi.fn(),
  deleteBucket: vi.fn(),
}));

vi.mock("../api/sites", () => ({
  listSites: vi.fn(),
}));

import { createBucket, deleteBucket, listBuckets } from "../api/buckets";
import { listSites } from "../api/sites";

function BucketsPageWithLocation() {
  const location = useLocation();

  return (
    <>
      <BucketsPage />
      <output data-testid="location-search">{location.search}</output>
    </>
  );
}

describe("BucketsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the create card, opens the dialog, and refreshes the grid", async () => {
    vi.mocked(listBuckets)
      .mockResolvedValueOnce({
        items: [
          {
            id: 1,
            name: "alpha",
            created_at: "2026-03-25T00:00:00Z",
            updated_at: "2026-03-25T00:00:00Z",
          },
        ],
      })
      .mockResolvedValueOnce({
        items: [
          {
            id: 1,
            name: "alpha",
            created_at: "2026-03-25T00:00:00Z",
            updated_at: "2026-03-25T00:00:00Z",
          },
          {
            id: 2,
            name: "beta",
            created_at: "2026-03-25T00:01:00Z",
            updated_at: "2026-03-25T00:01:00Z",
          },
        ],
      });
    vi.mocked(listSites).mockResolvedValue({ items: [] });
    vi.mocked(createBucket).mockResolvedValue({
      id: 2,
      name: "beta",
      created_at: "2026-03-25T00:01:00Z",
      updated_at: "2026-03-25T00:01:00Z",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets" element={<BucketsPage />} />
      </Routes>,
      { route: "/buckets" },
    );

    expect(
      await screen.findByRole("heading", { name: "bucket" }),
    ).toBeInTheDocument();
    expect(
      await screen.findByRole("link", { name: "alpha" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Create a new bucket" }),
    ).toBeInTheDocument();

    await userEvent.click(
      screen.getByRole("button", { name: "Create a new bucket" }),
    );

    expect(
      await screen.findByRole("heading", { name: "Create a new bucket" }),
    ).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText("bucket name"), "beta");
    await userEvent.click(
      screen.getByRole("button", { name: "Create bucket" }),
    );

    await waitFor(() => {
      expect(createBucket).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        "beta",
      );
    });

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    expect(
      await screen.findByRole("link", { name: "beta" }),
    ).toBeInTheDocument();
  });

  it("shows only the create card when the list is empty", async () => {
    vi.mocked(listBuckets).mockResolvedValueOnce({ items: [] });
    vi.mocked(listSites).mockResolvedValue({ items: [] });

    renderWithApp(
      <Routes>
        <Route path="/buckets" element={<BucketsPage />} />
      </Routes>,
      { route: "/buckets" },
    );

    expect(
      await screen.findByRole("button", { name: "Create a new bucket" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("No buckets yet")).not.toBeInTheDocument();
    expect(screen.getByText("0 total")).toBeInTheDocument();
  });

  it("requires typing the bucket name before deleting and shows linked sites", async () => {
    vi.mocked(listBuckets)
      .mockResolvedValueOnce({
        items: [
          {
            id: 1,
            name: "alpha",
            created_at: "2026-03-25T00:00:00Z",
            updated_at: "2026-03-25T00:00:00Z",
          },
        ],
      })
      .mockResolvedValueOnce({ items: [] });
    vi.mocked(listSites)
      .mockResolvedValueOnce({
        items: [
          {
            id: 9,
            bucket: "alpha",
            root_prefix: "docs/",
            enabled: true,
            index_document: "index.html",
            error_document: "",
            spa_fallback: true,
            domains: ["demo.localhost", "www.localhost"],
            created_at: "2026-03-25T00:00:00Z",
            updated_at: "2026-03-25T00:00:00Z",
          },
        ],
      })
      .mockResolvedValueOnce({ items: [] });
    vi.mocked(deleteBucket).mockResolvedValue(undefined);

    renderWithApp(
      <Routes>
        <Route path="/buckets" element={<BucketsPage />} />
      </Routes>,
      { route: "/buckets" },
    );

    const deleteButtons = await screen.findAllByRole("button", {
      name: "Delete",
    });
    await userEvent.click(deleteButtons[0]);

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Delete bucket?")).toBeInTheDocument();
    expect(
      within(dialog).getByText((_, element) => {
        return element?.textContent === "Root prefix: docs/";
      }),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("docs/")).toBeInTheDocument();
    expect(
      within(dialog).getByText((_, element) => {
        return (
          element?.textContent === "Domains: demo.localhost, www.localhost"
        );
      }),
    ).toBeInTheDocument();

    const confirmButton = within(dialog).getByRole("button", {
      name: "Delete bucket",
    });
    expect(confirmButton).toBeDisabled();

    await userEvent.type(
      within(dialog).getByLabelText("Type the bucket name to confirm"),
      "beta",
    );
    expect(confirmButton).toBeDisabled();

    await userEvent.clear(
      within(dialog).getByLabelText("Type the bucket name to confirm"),
    );
    await userEvent.type(
      within(dialog).getByLabelText("Type the bucket name to confirm"),
      "alpha",
    );
    expect(confirmButton).toBeEnabled();

    await userEvent.click(confirmButton);

    await waitFor(() => {
      expect(deleteBucket).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        "alpha",
      );
    });

    await waitFor(() => {
      expect(
        screen.queryByRole("link", { name: "alpha" }),
      ).not.toBeInTheDocument();
    });
  });

  it("disables bucket deletion when the site list fails to load", async () => {
    vi.mocked(listBuckets).mockResolvedValue({
      items: [
        {
          id: 1,
          name: "alpha",
          created_at: "2026-03-25T00:00:00Z",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
    });
    vi.mocked(listSites).mockRejectedValue(new Error("site load failed"));

    renderWithApp(
      <Routes>
        <Route path="/buckets" element={<BucketsPage />} />
      </Routes>,
      { route: "/buckets" },
    );

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("site load failed")).toBeInTheDocument();
    const deleteButtons = await screen.findAllByRole("button", {
      name: "Delete",
    });
    expect(deleteButtons[0]).toBeDisabled();
  });

  it("reads the search term from the URL and requests filtered buckets", async () => {
    vi.mocked(listBuckets).mockResolvedValue({
      items: [
        {
          id: 1,
          name: "alpha",
          created_at: "2026-03-25T00:00:00Z",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
    });
    vi.mocked(listSites).mockResolvedValue({ items: [] });

    renderWithApp(
      <Routes>
        <Route path="/buckets" element={<BucketsPage />} />
      </Routes>,
      { route: "/buckets?search=alp" },
    );

    await waitFor(() => {
      expect(listBuckets).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        { search: "alp" },
      );
    });

    expect(
      await screen.findByRole("link", { name: "alpha" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Search bucket" })).toHaveValue(
      "alp",
    );
  });

  it("updates the URL and refetches after the search form is submitted", async () => {
    vi.mocked(listBuckets)
      .mockResolvedValueOnce({
        items: [
          {
            id: 1,
            name: "alpha",
            created_at: "2026-03-25T00:00:00Z",
            updated_at: "2026-03-25T00:00:00Z",
          },
          {
            id: 2,
            name: "beta",
            created_at: "2026-03-26T00:00:00Z",
            updated_at: "2026-03-26T00:00:00Z",
          },
        ],
      })
      .mockResolvedValueOnce({
        items: [
          {
            id: 2,
            name: "beta",
            created_at: "2026-03-26T00:00:00Z",
            updated_at: "2026-03-26T00:00:00Z",
          },
        ],
      });
    vi.mocked(listSites).mockResolvedValue({ items: [] });

    renderWithApp(
      <Routes>
        <Route path="/buckets" element={<BucketsPageWithLocation />} />
      </Routes>,
      { route: "/buckets" },
    );

    const searchInput = await screen.findByRole("textbox", {
      name: "Search bucket",
    });
    await userEvent.type(searchInput, "beta");
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));

    await waitFor(() => {
      expect(screen.getByTestId("location-search")).toHaveTextContent(
        "?search=beta",
      );
    });

    await waitFor(() => {
      expect(listBuckets).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        { search: "beta" },
      );
    });

    expect(
      await screen.findByRole("link", { name: "beta" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: "alpha" }),
    ).not.toBeInTheDocument();
  });

  it("refreshes the bucket list from the search toolbar", async () => {
    vi.mocked(listBuckets)
      .mockResolvedValueOnce({
        items: [
          {
            id: 1,
            name: "alpha",
            created_at: "2026-03-25T00:00:00Z",
            updated_at: "2026-03-25T00:00:00Z",
          },
        ],
      })
      .mockResolvedValueOnce({
        items: [
          {
            id: 2,
            name: "beta",
            created_at: "2026-03-26T00:00:00Z",
            updated_at: "2026-03-26T00:00:00Z",
          },
        ],
      });
    vi.mocked(listSites).mockResolvedValue({ items: [] });

    renderWithApp(
      <Routes>
        <Route path="/buckets" element={<BucketsPage />} />
      </Routes>,
      { route: "/buckets" },
    );

    expect(
      await screen.findByRole("link", { name: "alpha" }),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Refresh" }));

    expect(
      await screen.findByRole("link", { name: "beta" }),
    ).toBeInTheDocument();
    expect(listBuckets).toHaveBeenCalledTimes(2);
  });

  it("shows a search-specific empty state and keeps the create action", async () => {
    vi.mocked(listBuckets).mockResolvedValue({ items: [] });
    vi.mocked(listSites).mockResolvedValue({ items: [] });

    renderWithApp(
      <Routes>
        <Route path="/buckets" element={<BucketsPage />} />
      </Routes>,
      { route: "/buckets?search=missing" },
    );

    expect(await screen.findByText("No matching bucket")).toBeInTheDocument();
    expect(
      screen.getByText("Try another keyword or create a new bucket."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Create a new bucket" }),
    ).toBeInTheDocument();
  });
});

const pinsKey = "light-oss-pinned-buckets:http://localhost:8080";
const pinBuckets = [
  {
    id: 1,
    name: "alpha",
    created_at: "2026-03-25T00:00:00Z",
    updated_at: "2026-03-25T00:00:00Z",
  },
  {
    id: 2,
    name: "beta",
    created_at: "2026-03-26T00:00:00Z",
    updated_at: "2026-03-26T00:00:00Z",
  },
];

function renderPinsPage(route = "/buckets") {
  return renderWithApp(
    <Routes>
      <Route path="/buckets" element={<BucketsPage />} />
    </Routes>,
    { route },
  );
}

function bucketNames(region: HTMLElement) {
  return within(region)
    .queryAllByRole("link")
    .map((link) => link.textContent)
    .filter((name) => name !== "Open");
}

describe("BucketsPage local pins", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    vi.mocked(listBuckets).mockImplementation(async (_settings, options) => ({
      items: pinBuckets.filter(
        (bucket) => !options?.search || bucket.name.includes(options.search),
      ),
    }));
    vi.mocked(listSites).mockResolvedValue({ items: [] });
  });

  afterEach(() => localStorage.clear());

  it("keeps the normal list unchanged and appends pins in action order", async () => {
    renderPinsPage();
    const list = screen.getByRole("region", { name: "bucket list" });
    await within(list).findByRole("link", { name: "alpha" });
    expect(
      screen.queryByRole("region", { name: "Pinned buckets" }),
    ).not.toBeInTheDocument();
    await userEvent.click(
      within(list).getByRole("button", { name: "Pin bucket beta" }),
    );
    await userEvent.click(
      within(list).getByRole("button", { name: "Pin bucket alpha" }),
    );
    const pinned = screen.getByRole("region", { name: "Pinned buckets" });
    expect(bucketNames(pinned)).toEqual(["beta", "alpha"]);
    expect(bucketNames(list)).toEqual(["alpha", "beta"]);
    expect(within(list).getByText("2 total")).toBeInTheDocument();
    expect(
      within(pinned).queryByRole("button", { name: "Create a new bucket" }),
    ).not.toBeInTheDocument();
    expect(listBuckets).toHaveBeenCalledTimes(1);

    await userEvent.click(
      within(pinned).getByRole("button", { name: "Unpin bucket beta" }),
    );
    expect(
      within(list).getByRole("button", { name: "Pin bucket beta" }),
    ).toHaveAttribute("aria-pressed", "false");
    await userEvent.click(
      within(list).getByRole("button", { name: "Pin bucket beta" }),
    );
    expect(bucketNames(pinned)).toEqual(["alpha", "beta"]);
    await userEvent.click(
      within(list).getByRole("button", { name: "Unpin bucket alpha" }),
    );
    await userEvent.click(
      within(pinned).getByRole("button", { name: "Unpin bucket beta" }),
    );
    expect(
      screen.queryByRole("region", { name: "Pinned buckets" }),
    ).not.toBeInTheDocument();
    expect(JSON.parse(localStorage.getItem(pinsKey)!)).toEqual([]);
  });

  it("restores pins on remount and supports keyboard activation", async () => {
    const first = renderPinsPage();
    const button = await screen.findByRole("button", {
      name: "Pin bucket alpha",
    });
    act(() => button.focus());
    await userEvent.keyboard("{Enter}");
    expect(
      screen.getByRole("region", { name: "Pinned buckets" }),
    ).toBeInTheDocument();
    first.unmount();
    renderPinsPage();
    const pinned = await screen.findByRole("region", {
      name: "Pinned buckets",
    });
    expect(bucketNames(pinned)).toEqual(["alpha"]);
    expect(
      within(pinned).getByRole("button", { name: "Unpin bucket alpha" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it.each(["beta", "missing"])(
    "keeps every pin when searching for %s",
    async (search) => {
      localStorage.setItem(pinsKey, "[1,2]");
      renderPinsPage(`/buckets?search=${search}`);
      const pinned = await screen.findByRole("region", {
        name: "Pinned buckets",
      });
      expect(bucketNames(pinned)).toEqual(["alpha", "beta"]);
      const list = screen.getByRole("region", { name: "bucket list" });
      await waitFor(() =>
        expect(bucketNames(list)).toEqual(search === "beta" ? ["beta"] : []),
      );
      expect(localStorage.getItem(pinsKey)).toBe("[1,2]");
      expect(listBuckets).toHaveBeenCalledWith(expect.anything(), {
        search: "",
      });
      expect(listBuckets).toHaveBeenCalledWith(expect.anything(), { search });
    },
  );

  it("does not request the full list for an unpinned search", async () => {
    renderPinsPage("/buckets?search=beta");
    await screen.findByRole("link", { name: "beta" });
    expect(listBuckets).toHaveBeenCalledTimes(1);
    expect(listBuckets).toHaveBeenCalledWith(expect.anything(), {
      search: "beta",
    });
  });

  it("loads the independent region when the first pin is added during search", async () => {
    renderPinsPage("/buckets?search=beta");
    await userEvent.click(
      await screen.findByRole("button", { name: "Pin bucket beta" }),
    );
    const pinned = await screen.findByRole("region", {
      name: "Pinned buckets",
    });
    expect(bucketNames(pinned)).toEqual(["beta"]);
    expect(listBuckets).toHaveBeenCalledWith(expect.anything(), { search: "" });
    expect(localStorage.getItem(pinsKey)).toBe("[2]");
  });

  it("retains stored pins when their independent query fails", async () => {
    localStorage.setItem(pinsKey, "[1]");
    vi.mocked(listBuckets).mockImplementation(async (_settings, options) => {
      if (!options?.search) throw new Error("full list unavailable");
      return { items: [pinBuckets[1]] };
    });
    renderPinsPage("/buckets?search=beta");
    expect(
      await screen.findByText("Failed to load pinned buckets"),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "beta" })).toBeInTheDocument();
    expect(localStorage.getItem(pinsKey)).toBe("[1]");
  });

  it("keeps existing cards and pins if refreshing the full list fails", async () => {
    localStorage.setItem(pinsKey, "[1]");
    renderPinsPage();
    await screen.findByRole("region", { name: "Pinned buckets" });
    vi.mocked(listBuckets).mockRejectedValue(new Error("refresh failed"));
    await userEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await screen.findByText("refresh failed");
    expect(localStorage.getItem(pinsKey)).toBe("[1]");
    expect(
      bucketNames(screen.getByRole("region", { name: "Pinned buckets" })),
    ).toEqual(["alpha"]);
  });

  it("removes stale IDs after a full refresh without pinning a same-name replacement", async () => {
    localStorage.setItem(pinsKey, "[1,2]");
    renderPinsPage();
    await screen.findByRole("region", { name: "Pinned buckets" });
    vi.mocked(listBuckets).mockResolvedValue({
      items: [{ ...pinBuckets[0], id: 3 }, pinBuckets[1]],
    });
    await userEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(localStorage.getItem(pinsKey)).toBe("[2]"));
    expect(
      bucketNames(screen.getByRole("region", { name: "Pinned buckets" })),
    ).toEqual(["beta"]);
    expect(
      screen.getByRole("button", { name: "Pin bucket alpha" }),
    ).toHaveAttribute("aria-pressed", "false");
  });

  it.each(["Pinned buckets", "bucket list"])(
    "deletes a pinned bucket from %s",
    async (regionName) => {
      localStorage.setItem(pinsKey, "[1]");
      vi.mocked(deleteBucket).mockImplementation(async () => {
        vi.mocked(listBuckets).mockResolvedValue({ items: [pinBuckets[1]] });
      });
      renderPinsPage();
      await screen.findByRole("region", { name: "Pinned buckets" });
      const region = screen.getByRole("region", { name: regionName });
      await userEvent.click(
        within(region).getAllByRole("button", { name: "Delete" })[0],
      );
      const dialog = await screen.findByRole("alertdialog");
      await userEvent.type(
        within(dialog).getByLabelText("Type the bucket name to confirm"),
        "alpha",
      );
      await userEvent.click(
        within(dialog).getByRole("button", { name: "Delete bucket" }),
      );
      await waitFor(() => expect(localStorage.getItem(pinsKey)).toBe("[]"));
      expect(
        screen.queryByRole("region", { name: "Pinned buckets" }),
      ).not.toBeInTheDocument();
      expect(deleteBucket).toHaveBeenCalledWith(expect.anything(), "alpha");
    },
  );

  it("retains a pin and confirmation dialog after deletion fails", async () => {
    localStorage.setItem(pinsKey, "[1]");
    vi.mocked(deleteBucket).mockRejectedValue(new Error("delete failed"));
    renderPinsPage();
    const pinned = await screen.findByRole("region", {
      name: "Pinned buckets",
    });
    await userEvent.click(
      within(pinned).getByRole("button", { name: "Delete" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    await userEvent.type(
      within(dialog).getByLabelText("Type the bucket name to confirm"),
      "alpha",
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Delete bucket" }),
    );
    await screen.findByText("delete failed");
    expect(dialog).toBeInTheDocument();
    expect(localStorage.getItem(pinsKey)).toBe("[1]");
  });

  it("removes a successfully deleted pin even when the subsequent refresh fails", async () => {
    localStorage.setItem(pinsKey, "[1]");
    vi.mocked(deleteBucket).mockImplementation(async () => {
      vi.mocked(listBuckets).mockRejectedValue(new Error("refresh failed"));
    });
    renderPinsPage();
    const pinned = await screen.findByRole("region", {
      name: "Pinned buckets",
    });
    await userEvent.click(
      within(pinned).getByRole("button", { name: "Delete" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    await userEvent.type(
      within(dialog).getByLabelText("Type the bucket name to confirm"),
      "alpha",
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Delete bucket" }),
    );
    await screen.findByText("refresh failed");
    await waitFor(() => expect(localStorage.getItem(pinsKey)).toBe("[]"));
    expect(
      screen.queryByRole("region", { name: "Pinned buckets" }),
    ).not.toBeInTheDocument();
  });
});
