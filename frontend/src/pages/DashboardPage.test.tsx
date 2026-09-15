import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, Route, Routes } from "react-router-dom";
import { beforeEach, describe, it, vi } from "vitest";
import { DashboardPage } from "./DashboardPage";
import { BucketsPage } from "./BucketsPage";
import { renderWithApp } from "../test/test-utils";

vi.mock("../api/buckets", () => ({
  listBuckets: vi.fn(),
  createBucket: vi.fn(),
  deleteBucket: vi.fn(),
}));

vi.mock("../api/system", () => ({
  getSystemStats: vi.fn(),
}));

vi.mock("../api/sites", () => ({ listSites: vi.fn() }));

import { listSites } from "../api/sites";
import { deleteBucket, listBuckets } from "../api/buckets";
import { getSystemStats } from "../api/system";

describe("DashboardPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders bucket overview and system metrics", async () => {
    vi.mocked(listBuckets).mockResolvedValueOnce({
      items: [
        {
          id: 1,
          name: "alpha",
          created_at: "2026-03-25T00:00:00Z",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
    });
    vi.mocked(getSystemStats).mockResolvedValueOnce({
      os: "linux",
      cpu: {
        used_percent: 27.3,
      },
      memory: {
        total_bytes: 8 * 1024 * 1024 * 1024,
        used_bytes: 4 * 1024 * 1024 * 1024,
        available_bytes: 4 * 1024 * 1024 * 1024,
        used_percent: 50,
      },
      disks: [
        {
          label: "C:",
          mount_point: "C:\\",
          filesystem: "NTFS",
          total_bytes: 100 * 1024 * 1024 * 1024,
          used_bytes: 40 * 1024 * 1024 * 1024,
          free_bytes: 60 * 1024 * 1024 * 1024,
          used_percent: 40,
          contains_storage_root: true,
        },
        {
          label: "D:",
          mount_point: "D:\\",
          filesystem: "NTFS",
          total_bytes: 200 * 1024 * 1024 * 1024,
          used_bytes: 50 * 1024 * 1024 * 1024,
          free_bytes: 150 * 1024 * 1024 * 1024,
          used_percent: 25,
          contains_storage_root: false,
        },
      ],
      storage: {
        root_path: "C:\\light-oss-data\\storage",
        used_bytes: 512 * 1024 * 1024,
        max_bytes: 10 * 1024 * 1024 * 1024,
        remaining_bytes: 9.5 * 1024 * 1024 * 1024,
        used_percent: 5,
        limit_status: "ok",
      },
    });

    renderWithApp(
      <Routes>
        <Route path="/dashboard" element={<DashboardPage />} />
      </Routes>,
      { route: "/dashboard" },
    );

    expect(
      await screen.findByRole("heading", { name: "Dashboard" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Total bucket")).toBeInTheDocument();
    expect(screen.getByText("API host overview")).toBeInTheDocument();
    expect(screen.getByText("Host OS")).toBeInTheDocument();
    expect(await screen.findByText("Linux")).toBeInTheDocument();
    expect(screen.getByText("CPU usage")).toBeInTheDocument();
    expect(await screen.findByText("27.3%")).toBeInTheDocument();
    expect(screen.getByText("Memory usage")).toBeInTheDocument();
    expect(await screen.findByText("50.0%")).toBeInTheDocument();
    expect(screen.getAllByText("OSS storage used").length).toBeGreaterThan(0);
    expect(
      (await screen.findAllByText("512.0 MB / 10.0 GB")).length,
    ).toBeGreaterThan(0);
    expect(screen.getByText("Disk usage")).toBeInTheDocument();
    expect(await screen.findByText("C:")).toBeInTheDocument();
    expect(screen.getAllByText("Storage root").length).toBeGreaterThan(0);
    expect(screen.queryByText("Storage limit warning")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Create bucket" }),
    ).not.toBeInTheDocument();
  });

  it("shows system stats error without hiding bucket overview", async () => {
    vi.mocked(listBuckets).mockResolvedValueOnce({
      items: [
        {
          id: 1,
          name: "alpha",
          created_at: "2026-03-25T00:00:00Z",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
    });
    vi.mocked(getSystemStats).mockRejectedValueOnce(
      Object.assign(new Error("system metrics unavailable"), {
        status: 500,
        code: "system_metrics_unavailable",
      }),
    );

    renderWithApp(
      <Routes>
        <Route path="/dashboard" element={<DashboardPage />} />
      </Routes>,
      { route: "/dashboard" },
    );

    expect(
      await screen.findByText("Failed to load system stats"),
    ).toBeInTheDocument();
    expect(screen.getByText("system metrics unavailable")).toBeInTheDocument();
    expect(screen.getByText("Total bucket")).toBeInTheDocument();
    expect(screen.getByText("API host overview")).toBeInTheDocument();
  });

  it("shows a warning alert when storage usage reaches the warning threshold", async () => {
    vi.mocked(listBuckets).mockResolvedValueOnce({ items: [] });
    vi.mocked(getSystemStats).mockResolvedValueOnce({
      os: "linux",
      cpu: {
        used_percent: 27.3,
      },
      memory: {
        total_bytes: 8 * 1024 * 1024 * 1024,
        used_bytes: 4 * 1024 * 1024 * 1024,
        available_bytes: 4 * 1024 * 1024 * 1024,
        used_percent: 50,
      },
      disks: [],
      storage: {
        root_path: "/data/storage",
        used_bytes: 8 * 1024 * 1024 * 1024,
        max_bytes: 10 * 1024 * 1024 * 1024,
        remaining_bytes: 2 * 1024 * 1024 * 1024,
        used_percent: 80,
        limit_status: "warning",
      },
    });

    renderWithApp(
      <Routes>
        <Route path="/dashboard" element={<DashboardPage />} />
      </Routes>,
      { route: "/dashboard" },
    );

    expect(
      await screen.findByText("Storage limit warning"),
    ).toBeInTheDocument();
  });

  it("shows a destructive alert when storage usage exceeds the limit", async () => {
    vi.mocked(listBuckets).mockResolvedValueOnce({ items: [] });
    vi.mocked(getSystemStats).mockResolvedValueOnce({
      os: "linux",
      cpu: {
        used_percent: 27.3,
      },
      memory: {
        total_bytes: 8 * 1024 * 1024 * 1024,
        used_bytes: 4 * 1024 * 1024 * 1024,
        available_bytes: 4 * 1024 * 1024 * 1024,
        used_percent: 50,
      },
      disks: [],
      storage: {
        root_path: "/data/storage",
        used_bytes: 12 * 1024 * 1024 * 1024,
        max_bytes: 10 * 1024 * 1024 * 1024,
        remaining_bytes: 0,
        used_percent: 120,
        limit_status: "exceeded",
      },
    });

    renderWithApp(
      <Routes>
        <Route path="/dashboard" element={<DashboardPage />} />
      </Routes>,
      { route: "/dashboard" },
    );

    expect(
      await screen.findByText("Storage limit exceeded"),
    ).toBeInTheDocument();
  });
});

const pinsKey = "light-oss-pinned-buckets:http://localhost:8080";
const buckets = [
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

function renderDashboard(
  route = "/dashboard",
  apiBaseUrl = "http://localhost:8080",
) {
  return renderWithApp(
    <>
      <nav>
        <Link to="/dashboard">Go dashboard</Link>
        <Link to="/buckets">Go buckets</Link>
      </nav>
      <Routes>
        <Route path="/dashboard" element={<DashboardPage />} />
        <Route path="/buckets" element={<BucketsPage />} />
      </Routes>
    </>,
    { route, settings: { apiBaseUrl, bearerToken: "dev-token" } },
  );
}

async function confirmPinnedDeletion() {
  const pinned = await screen.findByRole("region", { name: "Pinned buckets" });
  const button = within(pinned).getByRole("button", { name: "Delete" });
  await waitFor(() => expect(button).toBeEnabled());
  await userEvent.click(button);
  const dialog = await screen.findByRole("alertdialog");
  const confirm = within(dialog).getByRole("button", { name: "Delete bucket" });
  expect(confirm).toBeDisabled();
  await userEvent.type(
    within(dialog).getByLabelText("Type the bucket name to confirm"),
    "alpha",
  );
  await userEvent.click(confirm);
}

describe("DashboardPage pinned buckets", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    vi.mocked(listBuckets).mockResolvedValue({ items: buckets });
    vi.mocked(listSites).mockResolvedValue({ items: [] });
    vi.mocked(getSystemStats).mockResolvedValue({
      os: "linux",
      cpu: { used_percent: 10 },
      memory: {
        total_bytes: 100,
        used_bytes: 10,
        available_bytes: 90,
        used_percent: 10,
      },
      disks: [],
      storage: {
        root_path: "/data/storage",
        used_bytes: 10,
        max_bytes: 100,
        remaining_bytes: 90,
        used_percent: 10,
        limit_status: "ok",
      },
    });
  });
  afterEach(() => localStorage.clear());

  it("hides the module and skips sites when there are no pins", async () => {
    renderDashboard();
    await screen.findByText("2 total");
    expect(
      screen.queryByRole("region", { name: "Pinned buckets" }),
    ).not.toBeInTheDocument();
    expect(listSites).not.toHaveBeenCalled();
    expect(listBuckets).toHaveBeenCalledWith(expect.anything(), { search: "" });
  });

  it("places full cards between overview and system resources in stored order", async () => {
    localStorage.setItem(pinsKey, "[2,1]");
    renderDashboard();
    const pinned = await screen.findByRole("region", {
      name: "Pinned buckets",
    });
    expect(
      within(pinned)
        .getAllByRole("link")
        .filter((link) => link.textContent !== "Open")
        .map((link) => link.textContent),
    ).toEqual(["beta", "alpha"]);
    const overview = screen.getByText("Total bucket");
    const system = screen.getByRole("heading", { name: "API host overview" });
    expect(
      overview.compareDocumentPosition(pinned) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      pinned.compareDocumentPosition(system) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(within(pinned).getAllByText("Created")).toHaveLength(2);
    expect(
      within(pinned).getAllByRole("button", { name: "Delete" }),
    ).toHaveLength(2);
    expect(within(pinned).getByRole("link", { name: "alpha" })).toHaveAttribute(
      "href",
      "/buckets/alpha",
    );
    expect(
      screen.queryByRole("button", { name: "Create a new bucket" }),
    ).not.toBeInTheDocument();
  });

  it("hides the last unpinned card without changing overview data", async () => {
    localStorage.setItem(pinsKey, "[2]");
    renderDashboard();
    const pinned = await screen.findByRole("region", {
      name: "Pinned buckets",
    });
    await userEvent.click(
      within(pinned).getByRole("button", { name: "Unpin bucket beta" }),
    );
    expect(
      screen.queryByRole("region", { name: "Pinned buckets" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("2 total")).toBeInTheDocument();
    expect(screen.getByText("beta")).toBeInTheDocument();
    expect(localStorage.getItem(pinsKey)).toBe("[]");
  });

  it("shares changes when navigating between the bucket page and dashboard", async () => {
    renderDashboard("/buckets");
    await userEvent.click(
      await screen.findByRole("button", { name: "Pin bucket alpha" }),
    );
    await userEvent.click(screen.getByRole("link", { name: "Go dashboard" }));
    const pinned = await screen.findByRole("region", {
      name: "Pinned buckets",
    });
    expect(
      within(pinned).getByRole("link", { name: "alpha" }),
    ).toBeInTheDocument();
    await userEvent.click(
      within(pinned).getByRole("button", { name: "Unpin bucket alpha" }),
    );
    await userEvent.click(screen.getByRole("link", { name: "Go buckets" }));
    expect(
      await screen.findByRole("button", { name: "Pin bucket alpha" }),
    ).toHaveAttribute("aria-pressed", "false");
    expect(
      screen.queryByRole("region", { name: "Pinned buckets" }),
    ).not.toBeInTheDocument();
  });

  it("isolates different service addresses", async () => {
    localStorage.setItem(pinsKey, "[1]");
    localStorage.setItem("light-oss-pinned-buckets:http://other:8080", "[2]");
    renderDashboard("/dashboard", "http://other:8080/");
    const pinned = await screen.findByRole("region", {
      name: "Pinned buckets",
    });
    expect(
      within(pinned).getByRole("link", { name: "beta" }),
    ).toBeInTheDocument();
    expect(
      within(pinned).queryByRole("link", { name: "alpha" }),
    ).not.toBeInTheDocument();
    expect(localStorage.getItem(pinsKey)).toBe("[1]");
  });

  it("cleans missing IDs after a successful full query", async () => {
    localStorage.setItem(pinsKey, "[99,2]");
    renderDashboard();
    await waitFor(() => expect(localStorage.getItem(pinsKey)).toBe("[2]"));
    expect(
      within(screen.getByRole("region", { name: "Pinned buckets" })).getByRole(
        "link",
        { name: "beta" },
      ),
    ).toBeInTheDocument();
  });

  it("retains IDs when the bucket query fails", async () => {
    localStorage.setItem(pinsKey, "[99,2]");
    vi.mocked(listBuckets).mockRejectedValue(new Error("buckets unavailable"));
    renderDashboard();
    await screen.findByText("buckets unavailable");
    expect(localStorage.getItem(pinsKey)).toBe("[99,2]");
  });

  it("disables deletion while sites are loading", async () => {
    localStorage.setItem(pinsKey, "[1]");
    vi.mocked(listSites).mockReturnValue(new Promise(() => {}));
    renderDashboard();
    const pinned = await screen.findByRole("region", {
      name: "Pinned buckets",
    });
    expect(
      within(pinned).getByRole("button", { name: "Delete" }),
    ).toBeDisabled();
    expect(
      within(pinned).getByRole("button", { name: "Unpin bucket alpha" }),
    ).toBeEnabled();
  });

  it("shows site errors and disables deletion without hiding pins", async () => {
    localStorage.setItem(pinsKey, "[1]");
    vi.mocked(listSites).mockRejectedValue(new Error("sites unavailable"));
    renderDashboard();
    await screen.findByText("sites unavailable");
    expect(
      within(screen.getByRole("region", { name: "Pinned buckets" })).getByRole(
        "button",
        { name: "Delete" },
      ),
    ).toBeDisabled();
    expect(localStorage.getItem(pinsKey)).toBe("[1]");
  });

  it("shows linked sites, deletes the pin, and refreshes overview and bucket page", async () => {
    localStorage.setItem(pinsKey, "[1]");
    vi.mocked(listSites).mockResolvedValue({
      items: [
        {
          id: 9,
          bucket: "alpha",
          root_prefix: "docs/",
          enabled: true,
          index_document: "index.html",
          error_document: "",
          spa_fallback: false,
          domains: ["demo.localhost"],
          created_at: "2026-03-25T00:00:00Z",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
    });
    vi.mocked(deleteBucket).mockImplementation(async () => {
      expect(
        within(screen.getByRole("alertdialog")).getByText("docs/"),
      ).toBeInTheDocument();
      expect(
        within(screen.getByRole("alertdialog")).getByText("demo.localhost"),
      ).toBeInTheDocument();
      vi.mocked(listBuckets).mockResolvedValue({ items: [buckets[1]] });
    });
    renderDashboard();
    await confirmPinnedDeletion();
    await screen.findByText("1 total");
    expect(localStorage.getItem(pinsKey)).toBe("[]");
    expect(
      screen.queryByRole("region", { name: "Pinned buckets" }),
    ).not.toBeInTheDocument();
    expect(deleteBucket).toHaveBeenCalledWith(
      { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
      "alpha",
    );
    await userEvent.click(screen.getByRole("link", { name: "Go buckets" }));
    await screen.findByRole("link", { name: "beta" });
    expect(
      screen.queryByRole("link", { name: "alpha" }),
    ).not.toBeInTheDocument();
  });

  it("keeps the pin and dialog when deletion fails", async () => {
    localStorage.setItem(pinsKey, "[1]");
    vi.mocked(deleteBucket).mockRejectedValue(new Error("delete failed"));
    renderDashboard();
    await confirmPinnedDeletion();
    await screen.findByText("delete failed");
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(localStorage.getItem(pinsKey)).toBe("[1]");
    expect(screen.getByText("2 total")).toBeInTheDocument();
  });
});
