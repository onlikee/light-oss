import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { vi } from "vitest";
import { BucketObjectsPage } from "./BucketObjectsPage";
import { renderWithApp } from "../test/test-utils";

vi.mock("../api/objects", () => ({
  listExplorerEntries: vi.fn(),
  listRecycleBinObjects: vi.fn(),
  restoreRecycleBinObjects: vi.fn(),
  deleteRecycleBinObjects: vi.fn(),
  createFolder: vi.fn(),
  checkObjectExists: vi.fn(),
  uploadFolder: vi.fn(),
  uploadObject: vi.fn(),
  deleteExplorerEntriesBatch: vi.fn(),
  deleteObject: vi.fn(),
  deleteFolder: vi.fn(),
  downloadFolderZip: vi.fn(),
  updateObjectVisibility: vi.fn(),
  createSignedDownloadURL: vi.fn(),
  buildPublicObjectURL: vi.fn(() => "http://localhost:8080/download"),
}));

vi.mock("../lib/utils", async () => {
  const actual =
    await vi.importActual<typeof import("../lib/utils")>("../lib/utils");

  return {
    ...actual,
    downloadFile: vi.fn().mockResolvedValue(undefined),
  };
});

vi.mock("../api/sites", () => ({
  createSite: vi.fn(),
  publishObjectSite: vi.fn(),
  uploadFileAndPublishSite: vi.fn(),
  uploadAndPublishSite: vi.fn(),
}));

import {
  checkObjectExists,
  createFolder,
  createSignedDownloadURL,
  deleteRecycleBinObjects,
  deleteExplorerEntriesBatch,
  deleteFolder,
  deleteObject,
  downloadFolderZip,
  listRecycleBinObjects,
  restoreRecycleBinObjects,
  listExplorerEntries,
  uploadFolder,
  updateObjectVisibility,
  uploadObject,
} from "../api/objects";
import { downloadFile } from "../lib/utils";
import {
  createSite,
  publishObjectSite,
  uploadFileAndPublishSite,
  uploadAndPublishSite,
} from "../api/sites";

describe("BucketObjectsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(listRecycleBinObjects).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(restoreRecycleBinObjects).mockResolvedValue({
      restored_count: 0,
      failed_count: 0,
      failed_items: [],
    });
    vi.mocked(deleteRecycleBinObjects).mockResolvedValue({
      deleted_count: 0,
      failed_count: 0,
      failed_items: [],
    });
    vi.mocked(checkObjectExists).mockResolvedValue(false);
    vi.mocked(createSignedDownloadURL).mockResolvedValue({
      url: "http://localhost:8080/signed-download",
      expires_at: 1,
    });
    vi.mocked(downloadFile).mockResolvedValue(undefined);
    vi.mocked(deleteExplorerEntriesBatch).mockResolvedValue({
      deleted_count: 0,
      failed_count: 0,
      failed_items: [],
    });
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
    Object.defineProperty(window.navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    });
  });

  it("shows the bucket missing empty state instead of a generic error alert", async () => {
    vi.mocked(listExplorerEntries).mockRejectedValue(
      Object.assign(new Error("bucket not found"), {
        code: "bucket_not_found",
      }),
    );

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    expect(
      await screen.findByText("Open this page again from the bucket list."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Failed to load folder entries"),
    ).not.toBeInTheDocument();
  });

  it("shows the entries spinner while the initial entries request is pending", async () => {
    const emptyEntries = { items: [], next_cursor: "" };
    let resolveEntries: ((value: typeof emptyEntries) => void) | undefined;

    vi.mocked(listExplorerEntries).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveEntries = resolve;
        }),
    );

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    expect(
      (await screen.findAllByRole("status", { name: "Loading" })).length,
    ).toBeGreaterThan(0);

    resolveEntries?.(emptyEntries);

    expect(await screen.findByText("This folder is empty")).toBeInTheDocument();
    await waitFor(() => {
      expect(
        screen.queryByRole("status", { name: "Loading" }),
      ).not.toBeInTheDocument();
    });
  });

  it("renders initial entries content without waiting for a minimum spinner duration", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    const emptyEntries = { items: [], next_cursor: "" };
    let resolveEntries: ((value: typeof emptyEntries) => void) | undefined;

    try {
      vi.mocked(listExplorerEntries).mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveEntries = resolve;
          }),
      );

      renderWithApp(
        <Routes>
          <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
        </Routes>,
        { route: "/buckets/demo" },
      );

      expect(
        screen.getAllByRole("status", { name: "Loading" }).length,
      ).toBeGreaterThan(0);

      await act(async () => {
        resolveEntries?.(emptyEntries);
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(screen.getByText("This folder is empty")).toBeInTheDocument();
      expect(
        screen.queryByRole("status", { name: "Loading" }),
      ).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("opens a bucket-scoped recycle bin dialog from the explorer toolbar", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(listRecycleBinObjects).mockResolvedValue({
      items: [createRecycleBinItem()],
      next_cursor: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Recycle bin" }),
    );

    const dialog = await screen.findByRole("dialog");
    expect(
      await within(dialog).findByRole("heading", { name: "Recycle bin" }),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("docs/report.txt")).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("columnheader", { name: "Bucket" }),
    ).not.toBeInTheDocument();

    await waitFor(() => {
      expect(listRecycleBinObjects).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "demo",
          limit: 20,
          cursor: "",
        },
      );
    });
  });

  it("restores a recycle bin item for the current bucket without refetching the dialog", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(listRecycleBinObjects)
      .mockResolvedValueOnce({
        items: [createRecycleBinItem()],
        next_cursor: "",
      })
      .mockResolvedValue({
        items: [],
        next_cursor: "",
      });
    vi.mocked(restoreRecycleBinObjects).mockResolvedValue({
      restored_count: 1,
      failed_count: 0,
      failed_items: [],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Recycle bin" }),
    );

    const rowText = await screen.findByText("docs/report.txt");
    const row = rowText.closest("tr");
    expect(row).not.toBeNull();
    await userEvent.click(
      within(row as HTMLElement).getByRole("button", { name: "Restore" }),
    );

    const confirmDialog = await screen.findByRole("alertdialog");
    expect(
      within(confirmDialog).getByText("Restore recycle bin items?"),
    ).toBeInTheDocument();
    expect(restoreRecycleBinObjects).not.toHaveBeenCalled();
    await userEvent.click(
      within(confirmDialog).getByRole("button", { name: "Restore" }),
    );

    await waitFor(() => {
      expect(restoreRecycleBinObjects).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        [101],
      );
    });

    await waitFor(() => {
      expect(screen.queryByText("docs/report.txt")).not.toBeInTheDocument();
    });
    expect(listRecycleBinObjects).toHaveBeenCalledTimes(1);
  });

  it("shows restore failure reasons in a dialog", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(listRecycleBinObjects).mockResolvedValue({
      items: [createRecycleBinItem()],
      next_cursor: "",
    });
    vi.mocked(restoreRecycleBinObjects).mockResolvedValue({
      restored_count: 0,
      failed_count: 1,
      failed_items: [
        {
          id: 101,
          bucket_name: "demo",
          path: "docs/report.txt",
          code: "object_exists",
          message: "object already exists",
        },
      ],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Recycle bin" }),
    );

    const row = (await screen.findByText("docs/report.txt")).closest("tr");
    expect(row).not.toBeNull();
    await userEvent.click(
      within(row as HTMLElement).getByRole("button", { name: "Restore" }),
    );

    const confirmDialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(confirmDialog).getByRole("button", { name: "Restore" }),
    );

    expect(await screen.findByText("Restore failed")).toBeInTheDocument();
    expect(screen.getByText("Object already exists.")).toBeInTheDocument();
    expect(screen.queryByText(/object_exists/)).not.toBeInTheDocument();
    expect(screen.getAllByText("docs/report.txt")).toHaveLength(2);
    expect(listRecycleBinObjects).toHaveBeenCalledTimes(1);
  });

  it("localizes recycle bin restore failure reasons in Chinese", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(listRecycleBinObjects).mockResolvedValue({
      items: [createRecycleBinItem()],
      next_cursor: "",
    });
    vi.mocked(restoreRecycleBinObjects).mockResolvedValue({
      restored_count: 0,
      failed_count: 1,
      failed_items: [
        {
          id: 101,
          bucket_name: "demo",
          path: "docs/report.txt",
          code: "object_exists",
          message: "object already exists",
        },
      ],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      {
        route: "/buckets/demo",
        preferences: {
          locale: "zh-CN",
          theme: "light",
        },
      },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "回收站" }),
    );

    const row = (await screen.findByText("docs/report.txt")).closest("tr");
    expect(row).not.toBeNull();
    await userEvent.click(
      within(row as HTMLElement).getByRole("button", { name: "还原" }),
    );

    const confirmDialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(confirmDialog).getByRole("button", { name: "还原" }),
    );

    expect(await screen.findByText("还原失败")).toBeInTheDocument();
    expect(screen.getByText("目标路径已存在对象。")).toBeInTheDocument();
    expect(screen.queryByText("object already exists")).not.toBeInTheDocument();
    expect(screen.queryByText(/object_exists/)).not.toBeInTheDocument();
  });

  it("confirms permanent deletion for recycle bin items in the current bucket", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(listRecycleBinObjects)
      .mockResolvedValueOnce({
        items: [createRecycleBinItem()],
        next_cursor: "",
      })
      .mockResolvedValue({
        items: [],
        next_cursor: "",
      });
    vi.mocked(deleteRecycleBinObjects).mockResolvedValue({
      deleted_count: 1,
      failed_count: 0,
      failed_items: [],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Recycle bin" }),
    );

    const rowText = await screen.findByText("docs/report.txt");
    const row = rowText.closest("tr");
    expect(row).not.toBeNull();
    await userEvent.click(
      within(row as HTMLElement).getByRole("button", {
        name: "Delete permanently",
      }),
    );

    const confirmDialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(confirmDialog).getByRole("button", {
        name: "Delete permanently",
      }),
    );

    await waitFor(() => {
      expect(deleteRecycleBinObjects).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        [101],
      );
    });
  });

  it("restores a recycle bin directory row using its single visible item id", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(listRecycleBinObjects)
      .mockResolvedValueOnce({
        items: [
          createRecycleBinItem({
            type: "directory",
            path: "docs/",
            name: "docs",
            object_key: "docs/.light-oss-folder",
            size: 256,
            content_type: "application/x-directory",
            etag: "",
            visibility: "private",
          }),
        ],
        next_cursor: "",
      })
      .mockResolvedValue({
        items: [],
        next_cursor: "",
      });
    vi.mocked(restoreRecycleBinObjects).mockResolvedValue({
      restored_count: 1,
      failed_count: 0,
      failed_items: [],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Recycle bin" }),
    );

    const row = (await screen.findByText("docs/")).closest("tr");
    expect(row).not.toBeNull();
    await userEvent.click(
      within(row as HTMLElement).getByRole("button", { name: "Restore" }),
    );

    const confirmDialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(confirmDialog).getByRole("button", { name: "Restore" }),
    );

    await waitFor(() => {
      expect(restoreRecycleBinObjects).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        [101],
      );
    });
  });

  it("permanently deletes a recycle bin directory row using its single visible item id", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(listRecycleBinObjects)
      .mockResolvedValueOnce({
        items: [
          createRecycleBinItem({
            type: "directory",
            id: 202,
            path: "docs/",
            name: "docs",
            object_key: "docs/.light-oss-folder",
            size: 256,
            content_type: "application/x-directory",
            etag: "",
            visibility: "private",
          }),
        ],
        next_cursor: "",
      })
      .mockResolvedValue({
        items: [],
        next_cursor: "",
      });
    vi.mocked(deleteRecycleBinObjects).mockResolvedValue({
      deleted_count: 1,
      failed_count: 0,
      failed_items: [],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Recycle bin" }),
    );

    const row = (await screen.findByText("docs/")).closest("tr");
    expect(row).not.toBeNull();
    await userEvent.click(
      within(row as HTMLElement).getByRole("button", {
        name: "Delete permanently",
      }),
    );

    const confirmDialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(confirmDialog).getByRole("button", {
        name: "Delete permanently",
      }),
    );

    await waitFor(() => {
      expect(deleteRecycleBinObjects).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        [202],
      );
    });
  });

  it("supports batch recycle bin actions for the current bucket", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(listRecycleBinObjects)
      .mockResolvedValueOnce({
        items: [
          createRecycleBinItem(),
          createRecycleBinItem({
            id: 102,
            path: "docs/notes.txt",
            name: "notes.txt",
            object_key: "docs/notes.txt",
          }),
        ],
        next_cursor: "",
      })
      .mockResolvedValueOnce({
        items: [
          createRecycleBinItem({
            id: 102,
            path: "docs/notes.txt",
            name: "notes.txt",
            object_key: "docs/notes.txt",
          }),
        ],
        next_cursor: "",
      })
      .mockResolvedValue({
        items: [],
        next_cursor: "",
      });
    vi.mocked(restoreRecycleBinObjects).mockResolvedValue({
      restored_count: 1,
      failed_count: 0,
      failed_items: [],
    });
    vi.mocked(deleteRecycleBinObjects).mockResolvedValue({
      deleted_count: 1,
      failed_count: 0,
      failed_items: [],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Recycle bin" }),
    );

    await userEvent.click(
      await screen.findByRole("checkbox", { name: "Select report.txt" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Restore selected" }),
    );
    const restoreConfirmDialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(restoreConfirmDialog).getByRole("button", { name: "Restore" }),
    );

    await waitFor(() => {
      expect(restoreRecycleBinObjects).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        [101],
      );
    });

    await userEvent.click(
      await screen.findByRole("checkbox", { name: "Select notes.txt" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Delete selected" }),
    );
    const confirmDialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(confirmDialog).getByRole("button", {
        name: "Delete permanently",
      }),
    );

    await waitFor(() => {
      expect(deleteRecycleBinObjects).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        [102],
      );
    });
  });

  it("navigates into a directory from the table", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({
        items: [
          {
            type: "directory",
            path: "docs/",
            name: "docs",
            is_empty: false,
            object_key: null,
            original_filename: null,
            size: null,
            content_type: null,
            etag: null,
            visibility: null,
            updated_at: null,
          },
        ],
        next_cursor: "",
      })
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "docs/readme.txt",
            name: "readme.txt",
            is_empty: null,
            object_key: "docs/readme.txt",
            original_filename: "readme.txt",
            size: 12,
            content_type: "text/plain",
            etag: "abcdef1234567890",
            visibility: "public",
            updated_at: "2026-03-25T00:00:00Z",
          },
        ],
        next_cursor: "",
      });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    const tables = await screen.findAllByRole("table");
    const table = tables[tables.length - 1];

    expect(table).toBeDefined();
    await userEvent.click(within(table!).getByRole("button", { name: "docs" }));

    expect(await screen.findByText("readme.txt")).toBeInTheDocument();
    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          prefix: "docs/",
          search: "",
        }),
      );
    });
  });

  it("keeps previous rows covered while navigating into another directory", async () => {
    let resolveNestedDirectory:
      | ((value: { items: typeof nestedFileItems; next_cursor: string }) => void)
      | undefined;
    const nestedFileItems = [
      {
        type: "file" as const,
        path: "docs/readme.txt",
        name: "readme.txt",
        is_empty: null,
        object_key: "docs/readme.txt",
        original_filename: "readme.txt",
        size: 12,
        content_type: "text/plain",
        etag: "abcdef1234567890",
        visibility: "public" as const,
        updated_at: "2026-03-25T00:00:00Z",
      },
    ];

    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({
        items: [
          {
            type: "directory",
            path: "docs/",
            name: "docs",
            is_empty: false,
            object_key: null,
            original_filename: null,
            size: null,
            content_type: null,
            etag: null,
            visibility: null,
            updated_at: null,
          },
        ],
        next_cursor: "",
      })
      .mockImplementationOnce(
        async () =>
          await new Promise((resolve) => {
            resolveNestedDirectory = resolve;
          }),
      );

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    const tables = await screen.findAllByRole("table");
    const table = tables[tables.length - 1];

    expect(table).toBeDefined();
    await userEvent.click(within(table!).getByRole("button", { name: "docs" }));

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenCalledTimes(2);
    });
    expect(screen.getByRole("button", { name: "docs" })).toBeInTheDocument();
    expect(screen.queryByText("readme.txt")).not.toBeInTheDocument();
    expect(
      screen.getAllByRole("status", { name: "Loading" }).length,
    ).toBeGreaterThan(0);

    resolveNestedDirectory?.({
      items: nestedFileItems,
      next_cursor: "",
    });

    expect(await screen.findByText("readme.txt")).toBeInTheDocument();
  });

  it("applies explorer sorting from the popover and only refetches after confirmation", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "file",
          path: "docs/readme.txt",
          name: "readme.txt",
          is_empty: null,
          object_key: "docs/readme.txt",
          original_filename: "readme.txt",
          size: 12,
          content_type: "text/plain",
          etag: "abcdef1234567890",
          visibility: "public",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
      next_cursor: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?cursor=cursor-1" },
    );

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          cursor: "cursor-1",
          sortBy: "created_at",
          sortOrder: "desc",
        }),
      );
    });

    const initialCallCount = vi.mocked(listExplorerEntries).mock.calls.length;

    await userEvent.click(
      await screen.findByRole("button", { name: "Sort Size: not sorted" }),
    );

    const sizeTitle = await screen.findByText("Sort by Size");
    const sizePopover = sizeTitle.closest(
      "[data-slot='popover-content']",
    ) as HTMLElement | null;
    expect(sizePopover).not.toBeNull();
    expect(
      within(sizePopover!).getByText(
        "Choose an order and confirm to apply it.",
      ),
    ).toBeInTheDocument();

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenCalledTimes(initialCallCount);
    });

    await userEvent.click(
      within(sizePopover!).getByRole("radio", { name: "Descending" }),
    );

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenCalledTimes(initialCallCount);
    });

    await userEvent.click(
      within(sizePopover!).getByRole("button", { name: "Cancel" }),
    );

    await waitFor(() => {
      expect(screen.queryByText("Sort by Size")).not.toBeInTheDocument();
      expect(listExplorerEntries).toHaveBeenCalledTimes(initialCallCount);
    });

    await userEvent.click(
      screen.getByRole("button", { name: "Sort Size: not sorted" }),
    );

    const reopenedSizeTitle = await screen.findByText("Sort by Size");
    const reopenedSizePopover = reopenedSizeTitle.closest(
      "[data-slot='popover-content']",
    ) as HTMLElement | null;
    expect(reopenedSizePopover).not.toBeNull();

    await userEvent.click(
      within(reopenedSizePopover!).getByRole("radio", { name: "Ascending" }),
    );

    await userEvent.click(
      within(reopenedSizePopover!).getByRole("button", { name: "Apply" }),
    );

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          cursor: "",
          sortBy: "size",
          sortOrder: "asc",
        }),
      );
    });

    expect(
      await screen.findByRole("button", { name: "Sort Size: ascending" }),
    ).toBeInTheDocument();

    const appliedAscCallCount =
      vi.mocked(listExplorerEntries).mock.calls.length;

    await userEvent.click(
      screen.getByRole("button", { name: "Sort Size: ascending" }),
    );

    const descendingSizeTitle = await screen.findByText("Sort by Size");
    const descendingSizePopover = descendingSizeTitle.closest(
      "[data-slot='popover-content']",
    ) as HTMLElement | null;
    expect(descendingSizePopover).not.toBeNull();

    await userEvent.click(
      within(descendingSizePopover!).getByRole("radio", {
        name: "Descending",
      }),
    );

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenCalledTimes(appliedAscCallCount);
    });

    await userEvent.click(
      within(descendingSizePopover!).getByRole("button", { name: "Apply" }),
    );

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          cursor: "",
          sortBy: "size",
          sortOrder: "desc",
        }),
      );
    });

    await userEvent.click(
      await screen.findByRole("button", { name: "Sort Created: not sorted" }),
    );

    const createdTitle = await screen.findByText("Sort by Created");
    const createdPopover = createdTitle.closest(
      "[data-slot='popover-content']",
    ) as HTMLElement | null;
    expect(createdPopover).not.toBeNull();

    await userEvent.click(
      within(createdPopover!).getByRole("radio", { name: "Ascending" }),
    );

    await userEvent.click(
      within(createdPopover!).getByRole("button", { name: "Apply" }),
    );

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          cursor: "",
          sortBy: "created_at",
          sortOrder: "asc",
        }),
      );
    });

    await userEvent.click(
      screen.getByRole("button", { name: "Sort Created: ascending" }),
    );

    const clearTitle = await screen.findByText("Sort by Created");
    const clearPopover = clearTitle.closest(
      "[data-slot='popover-content']",
    ) as HTMLElement | null;
    expect(clearPopover).not.toBeNull();

    await userEvent.click(
      within(clearPopover!).getByRole("button", { name: "Clear sorting" }),
    );

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          cursor: "",
          sortBy: "created_at",
          sortOrder: "desc",
        }),
      );
    });
  });

  it("downloads a directory archive and only disables the active row action", async () => {
    let resolveDownload: (() => void) | undefined;
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "directory",
          path: "docs/",
          name: "docs",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
        {
          type: "directory",
          path: "images/",
          name: "images",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
      ],
      next_cursor: "",
    });
    vi.mocked(downloadFolderZip).mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveDownload = resolve;
        }),
    );

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    const buttons = await screen.findAllByRole("button", {
      name: "Download ZIP",
    });
    await userEvent.click(buttons[0]);

    await waitFor(() => {
      expect(downloadFolderZip).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        "demo",
        "docs/",
      );
    });

    const downloadingButtons = await screen.findAllByRole("button", {
      name: "Downloading ZIP...",
    });
    expect(downloadingButtons[0]).toBeDisabled();
    expect(buttons[1]).not.toBeDisabled();

    resolveDownload?.();
    await waitFor(() => {
      expect(
        screen.getAllByRole("button", { name: "Download ZIP" }),
      ).toHaveLength(2);
    });
  });

  it("shows an error toast when folder archive download fails", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "directory",
          path: "docs/",
          name: "docs",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
      ],
      next_cursor: "",
    });
    vi.mocked(downloadFolderZip).mockRejectedValue(new Error("archive failed"));

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Download ZIP" }),
    );

    expect(await screen.findByText("archive failed")).toBeInTheDocument();
  });

  it("downloads private files through a signed URL without opening a new tab", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "file",
          path: "docs/private.txt",
          name: "private.txt",
          is_empty: null,
          object_key: "docs/private.txt",
          original_filename: "private-report.txt",
          size: 7,
          content_type: "text/plain",
          etag: "etag",
          visibility: "private",
          updated_at: "2026-04-07T01:00:00Z",
        },
      ],
      next_cursor: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Signed download" }),
    );

    await waitFor(() => {
      expect(createSignedDownloadURL).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        "demo",
        "docs/private.txt",
        300,
      );
      expect(downloadFile).toHaveBeenCalledWith(
        "http://localhost:8080/signed-download?download=true",
        "private-report.txt",
      );
    });
  });

  it("bulk downloads mixed selections in table order and keeps the selection", async () => {
    const events: string[] = [];
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "file",
          path: "alpha.txt",
          name: "alpha.txt",
          is_empty: null,
          object_key: "alpha.txt",
          original_filename: "alpha-report.txt",
          size: 5,
          content_type: "text/plain",
          etag: "etag-a",
          visibility: "public",
          updated_at: "2026-04-07T01:00:00Z",
        },
        {
          type: "directory",
          path: "docs/",
          name: "docs",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
      ],
      next_cursor: "",
    });
    vi.mocked(downloadFile).mockImplementation(async (url, filename) => {
      events.push(`file:${filename}:${url}`);
    });
    vi.mocked(downloadFolderZip).mockImplementation(async () => {
      events.push("directory:docs/");
      throw new Error("archive failed");
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("checkbox", { name: "Select alpha.txt" }),
    );
    await userEvent.click(
      screen.getByRole("checkbox", { name: "Select docs" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Download selected" }),
    );

    await waitFor(() => {
      expect(events).toEqual([
        "file:alpha-report.txt:http://localhost:8080/download?download=true",
        "directory:docs/",
      ]);
    });

    expect(
      await screen.findByText("Downloaded 1 items, 1 failed"),
    ).toBeInTheDocument();
    expect(screen.getByText("2 items selected")).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Select alpha.txt" }),
    ).toHaveAttribute("data-state", "checked");
    expect(
      screen.getByRole("checkbox", { name: "Select docs" }),
    ).toHaveAttribute("data-state", "checked");
  });

  it("renders bulk actions inside the footer alongside the pagination summary", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "file",
          path: "alpha.txt",
          name: "alpha.txt",
          is_empty: null,
          object_key: "alpha.txt",
          original_filename: "alpha.txt",
          size: 5,
          content_type: "text/plain",
          etag: "etag-a",
          visibility: "public",
          updated_at: "2026-04-07T01:00:00Z",
        },
        {
          type: "directory",
          path: "docs/",
          name: "docs",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
      ],
      next_cursor: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("checkbox", { name: "Select alpha.txt" }),
    );

    const summary = await screen.findByText("Showing 2 items / Page 1");
    const footer = summary.closest(".border-t");

    expect(footer).not.toBeNull();
    expect(
      within(footer as HTMLElement).getByText("1 items selected"),
    ).toBeInTheDocument();
    expect(
      within(footer as HTMLElement).getByRole("button", {
        name: "Download selected",
      }),
    ).toBeInTheDocument();
    expect(
      within(footer as HTMLElement).getByRole("button", {
        name: "Delete selected",
      }),
    ).toBeInTheDocument();
  });

  it("submits mixed selected entries to bulk delete and clears selection after success", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "alpha.txt",
            name: "alpha.txt",
            is_empty: null,
            object_key: "alpha.txt",
            original_filename: "alpha.txt",
            size: 5,
            content_type: "text/plain",
            etag: "etag-a",
            visibility: "public",
            updated_at: "2026-04-07T01:00:00Z",
          },
          {
            type: "directory",
            path: "docs/",
            name: "docs",
            is_empty: false,
            object_key: null,
            original_filename: null,
            size: null,
            content_type: null,
            etag: null,
            visibility: null,
            updated_at: null,
          },
        ],
        next_cursor: "",
      })
      .mockResolvedValueOnce({
        items: [],
        next_cursor: "",
      });
    vi.mocked(deleteExplorerEntriesBatch).mockResolvedValue({
      deleted_count: 2,
      failed_count: 0,
      failed_items: [],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("checkbox", { name: "Select alpha.txt" }),
    );
    await userEvent.click(
      screen.getByRole("checkbox", { name: "Select docs" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Delete selected" }),
    );
    await userEvent.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Delete selected",
      }),
    );

    await waitFor(() => {
      expect(deleteExplorerEntriesBatch).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        "demo",
        [
          { type: "file", path: "alpha.txt" },
          { type: "directory", path: "docs/" },
        ],
      );
    });

    expect(await screen.findByText("2 items deleted")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.queryByText("2 items selected")).not.toBeInTheDocument();
    });
  });

  it("shows every selected entry in the bulk delete dialog with constrained names and truncated paths", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "file",
          path: "alpha.txt",
          name: "alpha.txt",
          is_empty: null,
          object_key: "alpha.txt",
          original_filename: "alpha.txt",
          size: 5,
          content_type: "text/plain",
          etag: "etag-a",
          visibility: "public",
          updated_at: "2026-04-07T01:00:00Z",
        },
        {
          type: "directory",
          path: "docs/",
          name: "docs",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
        {
          type: "file",
          path: "articles/2026/meeting.md",
          name: "meeting.md",
          is_empty: null,
          object_key: "articles/2026/meeting.md",
          original_filename: "meeting.md",
          size: 48,
          content_type: "text/markdown",
          etag: "etag-m",
          visibility: "private",
          updated_at: "2026-04-07T01:00:00Z",
        },
        {
          type: "directory",
          path: "assets/icons/",
          name: "icons",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
      ],
      next_cursor: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("checkbox", { name: "Select alpha.txt" }),
    );
    await userEvent.click(
      screen.getByRole("checkbox", { name: "Select docs" }),
    );
    await userEvent.click(
      screen.getByRole("checkbox", { name: "Select meeting.md" }),
    );
    await userEvent.click(
      screen.getByRole("checkbox", { name: "Select icons" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Delete selected" }),
    );

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveClass("overflow-hidden");
    expect(dialog).toHaveClass("data-[size=sm]:max-w-[calc(100vw-1.5rem)]");
    expect(dialog).toHaveClass("sm:data-[size=sm]:max-w-md");
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(4);
    expect(within(dialog).getAllByRole("listitem")[1]).toHaveClass(
      "grid-cols-[auto_minmax(0,1fr)]",
    );
    expect(within(dialog).getAllByText("alpha.txt")).toHaveLength(2);
    expect(within(dialog).getByText("docs")).toBeInTheDocument();
    expect(within(dialog).getByText("docs/")).toBeInTheDocument();
    const meetingName = within(dialog).getByText("meeting.md");
    expect(meetingName).toHaveClass("min-w-0");
    expect(meetingName).toHaveClass("max-w-full");
    expect(meetingName).toHaveClass("break-all");
    expect(meetingName).toHaveClass("[display:-webkit-box]");
    expect(meetingName).toHaveClass("[-webkit-line-clamp:3]");
    expect(within(dialog).getByText("articles/2026/meeting.md")).toHaveClass(
      "min-w-0",
    );
    expect(within(dialog).getByText("articles/2026/meeting.md")).toHaveClass(
      "truncate",
    );
    expect(within(dialog).getByText("icons")).toBeInTheDocument();
    expect(within(dialog).getByText("assets/icons/")).toBeInTheDocument();
    expect(
      within(dialog).queryByText("And 1 more selected items."),
    ).not.toBeInTheDocument();
  });

  it("keeps only failed entries selected after a partial bulk delete", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "alpha.txt",
            name: "alpha.txt",
            is_empty: null,
            object_key: "alpha.txt",
            original_filename: "alpha.txt",
            size: 5,
            content_type: "text/plain",
            etag: "etag-a",
            visibility: "public",
            updated_at: "2026-04-07T01:00:00Z",
          },
          {
            type: "file",
            path: "beta.txt",
            name: "beta.txt",
            is_empty: null,
            object_key: "beta.txt",
            original_filename: "beta.txt",
            size: 4,
            content_type: "text/plain",
            etag: "etag-b",
            visibility: "public",
            updated_at: "2026-04-07T01:00:00Z",
          },
        ],
        next_cursor: "",
      })
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "beta.txt",
            name: "beta.txt",
            is_empty: null,
            object_key: "beta.txt",
            original_filename: "beta.txt",
            size: 4,
            content_type: "text/plain",
            etag: "etag-b",
            visibility: "public",
            updated_at: "2026-04-07T01:00:00Z",
          },
        ],
        next_cursor: "",
      });
    vi.mocked(deleteExplorerEntriesBatch).mockResolvedValue({
      deleted_count: 1,
      failed_count: 1,
      failed_items: [
        {
          type: "file",
          path: "beta.txt",
          code: "object_delete_failed",
          message: "failed to delete",
        },
      ],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("checkbox", { name: "Select alpha.txt" }),
    );
    await userEvent.click(
      screen.getByRole("checkbox", { name: "Select beta.txt" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Delete selected" }),
    );
    await userEvent.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Delete selected",
      }),
    );

    expect(
      await screen.findByText("Deleted 1 items, 1 failed"),
    ).toBeInTheDocument();
    expect(await screen.findByText("1 items selected")).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Select beta.txt" }),
    ).toHaveAttribute("data-state", "checked");
  });

  it("supports upload flow in the current folder", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({ items: [], next_cursor: "" })
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "docs/new.txt",
            name: "new.txt",
            is_empty: null,
            object_key: "docs/new.txt",
            original_filename: "new.txt",
            size: 16,
            content_type: "text/plain",
            etag: "feedface12345678",
            visibility: "private",
            updated_at: "2026-03-25T00:02:00Z",
          },
        ],
        next_cursor: "",
      });

    vi.mocked(uploadObject).mockImplementation(async (_settings, params) => {
      params.onProgress?.(50);
      params.onProgress?.(100);
      return {
        id: 2,
        bucket_name: "demo",
        object_key: "docs/new.txt",
        original_filename: "new.txt",
        size: 16,
        content_type: "text/plain",
        etag: "feedface12345678",
        visibility: "private",
        created_at: "2026-03-25T00:02:00Z",
        updated_at: "2026-03-25T00:02:00Z",
      };
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload" }),
    );

    const file = new File(["hello"], "new.txt", { type: "text/plain" });
    await userEvent.upload(await screen.findByLabelText("File"), file);
    await userEvent.type(screen.getByLabelText("Object name"), "new.txt");
    await userEvent.click(screen.getByRole("button", { name: "Start upload" }));

    await waitFor(() => {
      expect(checkObjectExists).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        "demo",
        "docs/new.txt",
      );
      expect(uploadObject).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          objectKey: "docs/new.txt",
        }),
      );
    });

    expect(await screen.findByText("new.txt")).toBeInTheDocument();
  });

  it("keeps the existing table visible while entries refetch after upload", async () => {
    const existingItem = {
      type: "file" as const,
      path: "docs/existing.txt",
      name: "existing.txt",
      is_empty: null,
      object_key: "docs/existing.txt",
      original_filename: "existing.txt",
      size: 12,
      content_type: "text/plain",
      etag: "existing12345678",
      visibility: "private" as const,
      updated_at: "2026-03-25T00:01:00Z",
    };
    const uploadedItem = {
      type: "file" as const,
      path: "docs/new.txt",
      name: "new.txt",
      is_empty: null,
      object_key: "docs/new.txt",
      original_filename: "new.txt",
      size: 16,
      content_type: "text/plain",
      etag: "feedface12345678",
      visibility: "private" as const,
      updated_at: "2026-03-25T00:02:00Z",
    };
    let listExplorerEntriesCallCount = 0;
    let resolveRefetch:
      | ((value: { items: typeof uploadedItem[]; next_cursor: string }) => void)
      | undefined;

    vi.mocked(listExplorerEntries).mockImplementation(async () => {
      listExplorerEntriesCallCount += 1;

      if (listExplorerEntriesCallCount === 1) {
        return { items: [existingItem], next_cursor: "" };
      }

      if (listExplorerEntriesCallCount === 2) {
        return await new Promise((resolve) => {
          resolveRefetch = resolve;
        });
      }

      return { items: [existingItem, uploadedItem], next_cursor: "" };
    });

    vi.mocked(uploadObject).mockResolvedValue({
      id: 2,
      bucket_name: "demo",
      object_key: "docs/new.txt",
      original_filename: "new.txt",
      size: 16,
      content_type: "text/plain",
      etag: "feedface12345678",
      visibility: "private",
      created_at: "2026-03-25T00:02:00Z",
      updated_at: "2026-03-25T00:02:00Z",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    expect(await screen.findByText("existing.txt")).toBeInTheDocument();

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload" }),
    );

    const file = new File(["hello"], "new.txt", { type: "text/plain" });
    await userEvent.upload(await screen.findByLabelText("File"), file);
    await userEvent.type(screen.getByLabelText("Object name"), "new.txt");
    await userEvent.click(screen.getByRole("button", { name: "Start upload" }));

    await waitFor(() => {
      expect(uploadObject).toHaveBeenCalledTimes(1);
      expect(listExplorerEntries).toHaveBeenCalledTimes(2);
    });

    expect(screen.getByText("existing.txt")).toBeInTheDocument();
    expect(document.querySelectorAll("table")).toHaveLength(2);
    expect(
      document.querySelectorAll('svg[role="status"][aria-label="Loading"]')
        .length,
    ).toBeGreaterThan(0);

    resolveRefetch?.({
      items: [existingItem, uploadedItem],
      next_cursor: "",
    });

    expect(await screen.findByText("new.txt")).toBeInTheDocument();
  });

  it("shows only the entries loading overlay while entries refetch", async () => {
    const existingItem = {
      type: "file" as const,
      path: "docs/existing.txt",
      name: "existing.txt",
      is_empty: null,
      object_key: "docs/existing.txt",
      original_filename: "existing.txt",
      size: 12,
      content_type: "text/plain",
      etag: "existing12345678",
      visibility: "private" as const,
      updated_at: "2026-03-25T00:01:00Z",
    };
    const refreshedItems = [
      {
        type: "file" as const,
        path: "docs/refreshed.txt",
        name: "refreshed.txt",
        is_empty: null,
        object_key: "docs/refreshed.txt",
        original_filename: "refreshed.txt",
        size: 16,
        content_type: "text/plain",
        etag: "refreshed123456",
        visibility: "private" as const,
        updated_at: "2026-03-25T00:02:00Z",
      },
    ];
    let resolveRefetch:
      | ((value: { items: typeof refreshedItems; next_cursor: string }) => void)
      | undefined;

    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({ items: [existingItem], next_cursor: "" })
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveRefetch = resolve;
          }),
      );

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    expect(await screen.findByText("existing.txt")).toBeInTheDocument();

    const refreshButton = screen.getByRole("button", { name: "Refresh" });
    await userEvent.click(refreshButton);

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenCalledTimes(2);
    });
    expect(
      within(refreshButton).queryByRole("status", { name: "Loading" }),
    ).not.toBeInTheDocument();
    expect(screen.getAllByRole("status", { name: "Loading" })).toHaveLength(1);
    expect(screen.getByText("existing.txt")).toBeInTheDocument();

    resolveRefetch?.({
      items: refreshedItems,
      next_cursor: "",
    });

    expect(await screen.findByText("refreshed.txt")).toBeInTheDocument();
    await waitFor(() => {
      expect(
        screen.queryByRole("status", { name: "Loading" }),
      ).not.toBeInTheDocument();
    });
  });

  it("prompts overwrite before object upload when a conflict is detected", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({ items: [], next_cursor: "" })
      .mockResolvedValue({
        items: [
          {
            type: "file",
            path: "docs/new.txt",
            name: "new.txt",
            is_empty: null,
            object_key: "docs/new.txt",
            original_filename: "new.txt",
            size: 16,
            content_type: "text/plain",
            etag: "feedface12345678",
            visibility: "private",
            updated_at: "2026-03-25T00:02:00Z",
          },
        ],
        next_cursor: "",
      });

    let resolveOverwriteUpload: (() => void) | undefined;

    vi.mocked(checkObjectExists).mockResolvedValueOnce(true);
    vi.mocked(uploadObject).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOverwriteUpload = () =>
            resolve({
              id: 2,
              bucket_name: "demo",
              object_key: "docs/new.txt",
              original_filename: "new.txt",
              size: 16,
              content_type: "text/plain",
              etag: "feedface12345678",
              visibility: "private",
              created_at: "2026-03-25T00:02:00Z",
              updated_at: "2026-03-25T00:02:00Z",
            });
        }),
    );

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload" }),
    );

    const file = new File(["hello"], "new.txt", { type: "text/plain" });
    await userEvent.upload(await screen.findByLabelText("File"), file);
    await userEvent.type(screen.getByLabelText("Object name"), "new.txt");
    await userEvent.click(screen.getByRole("button", { name: "Start upload" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByText("Overwrite existing files?"),
    ).toBeInTheDocument();

    expect(uploadObject).not.toHaveBeenCalled();

    await userEvent.click(
      within(dialog).getByRole("button", { name: "Overwrite and upload" }),
    );

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });

    resolveOverwriteUpload?.();

    await waitFor(() => {
      expect(uploadObject).toHaveBeenCalledTimes(1);
      expect(uploadObject).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          objectKey: "docs/new.txt",
          allowOverwrite: true,
        }),
      );
    });

    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Start upload" }),
      ).not.toBeInTheDocument();
    });
  });

  it("cancels object upload when preflight conflict prompt is dismissed", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(checkObjectExists).mockResolvedValueOnce(true);

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload" }),
    );

    const file = new File(["hello"], "new.txt", { type: "text/plain" });
    await userEvent.upload(await screen.findByLabelText("File"), file);
    await userEvent.type(screen.getByLabelText("Object name"), "new.txt");
    await userEvent.click(screen.getByRole("button", { name: "Start upload" }));

    const dialog = await screen.findByRole("alertdialog");
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Cancel" }),
    );

    await waitFor(() => {
      expect(uploadObject).not.toHaveBeenCalled();
    });
  });

  it("supports folder upload flow in the current folder", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({ items: [], next_cursor: "" })
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "docs/assets/readme.txt",
            name: "readme.txt",
            is_empty: null,
            object_key: "docs/assets/readme.txt",
            original_filename: "readme.txt",
            size: 16,
            content_type: "text/plain",
            etag: "feedface12345678",
            visibility: "private",
            updated_at: "2026-03-25T00:02:00Z",
          },
        ],
        next_cursor: "",
      });

    vi.mocked(uploadFolder).mockResolvedValue({
      uploaded_count: 2,
      items: [
        {
          id: 2,
          bucket_name: "demo",
          object_key: "docs/assets/readme.txt",
          original_filename: "readme.txt",
          size: 16,
          content_type: "text/plain",
          etag: "feedface12345678",
          visibility: "private",
          created_at: "2026-03-25T00:02:00Z",
          updated_at: "2026-03-25T00:02:00Z",
        },
        {
          id: 3,
          bucket_name: "demo",
          object_key: "docs/assets/images/logo.png",
          original_filename: "logo.png",
          size: 24,
          content_type: "image/png",
          etag: "deadbeef12345678",
          visibility: "private",
          created_at: "2026-03-25T00:02:00Z",
          updated_at: "2026-03-25T00:02:00Z",
        },
      ],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload folder" }),
    );

    const readme = new File(["hello"], "readme.txt", { type: "text/plain" });
    const logo = new File(["png"], "logo.png", { type: "image/png" });
    Object.defineProperty(readme, "webkitRelativePath", {
      configurable: true,
      value: "assets/readme.txt",
    });
    Object.defineProperty(logo, "webkitRelativePath", {
      configurable: true,
      value: "assets/images/logo.png",
    });

    await userEvent.upload(await screen.findByLabelText("Folder"), [
      readme,
      logo,
    ]);
    await userEvent.click(
      screen.getByRole("button", { name: "Start folder upload" }),
    );

    await waitFor(() => {
      expect(uploadFolder).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          prefix: "docs/",
          files: [readme, logo],
          allowOverwrite: true,
        }),
      );
    });
    expect(checkObjectExists).not.toHaveBeenCalled();

    expect(await screen.findByText("readme.txt")).toBeInTheDocument();
  });

  it("uploads folder directly with overwrite enabled when a conflict exists", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({ items: [], next_cursor: "" })
      .mockResolvedValue({
        items: [
          {
            type: "file",
            path: "docs/assets/readme.txt",
            name: "readme.txt",
            is_empty: null,
            object_key: "docs/assets/readme.txt",
            original_filename: "readme.txt",
            size: 16,
            content_type: "text/plain",
            etag: "feedface12345678",
            visibility: "private",
            updated_at: "2026-03-25T00:02:00Z",
          },
        ],
        next_cursor: "",
      });

    vi.mocked(uploadFolder).mockResolvedValueOnce({
      uploaded_count: 1,
      items: [
        {
          id: 2,
          bucket_name: "demo",
          object_key: "docs/assets/readme.txt",
          original_filename: "readme.txt",
          size: 16,
          content_type: "text/plain",
          etag: "feedface12345678",
          visibility: "private",
          created_at: "2026-03-25T00:02:00Z",
          updated_at: "2026-03-25T00:02:00Z",
        },
      ],
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload folder" }),
    );

    const readme = new File(["hello"], "readme.txt", { type: "text/plain" });
    Object.defineProperty(readme, "webkitRelativePath", {
      configurable: true,
      value: "assets/readme.txt",
    });

    await userEvent.upload(await screen.findByLabelText("Folder"), [readme]);
    await userEvent.click(
      screen.getByRole("button", { name: "Start folder upload" }),
    );

    await waitFor(() => {
      expect(uploadFolder).toHaveBeenCalledTimes(1);
      expect(uploadFolder).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          prefix: "docs/",
          files: [readme],
          allowOverwrite: true,
        }),
      );
    });
    expect(checkObjectExists).not.toHaveBeenCalled();
  });

  it("uploads a folder and publishes a site from the toolbar", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({ items: [], next_cursor: "" })
      .mockResolvedValueOnce({
        items: [
          {
            type: "directory",
            path: "docs/dist/",
            name: "dist",
            is_empty: false,
            object_key: null,
            original_filename: null,
            size: null,
            content_type: null,
            etag: null,
            visibility: null,
            updated_at: null,
          },
        ],
        next_cursor: "",
      });
    vi.mocked(uploadAndPublishSite).mockResolvedValue({
      uploaded_count: 2,
      site: {
        id: 8,
        bucket: "demo",
        root_prefix: "docs/dist/",
        enabled: true,
        index_document: "index.html",
        error_document: "",
        spa_fallback: true,
        domains: ["demo.localhost"],
        created_at: "2026-03-30T00:00:00Z",
        updated_at: "2026-03-30T00:00:00Z",
      },
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload and publish" }),
    );

    const dialog = await screen.findByRole("dialog");
    const indexFile = new File(["<html>home</html>"], "index.html", {
      type: "text/html",
    });
    const appFile = new File(["console.log('demo')"], "app.js", {
      type: "application/javascript",
    });
    Object.defineProperty(indexFile, "webkitRelativePath", {
      configurable: true,
      value: "dist/index.html",
    });
    Object.defineProperty(appFile, "webkitRelativePath", {
      configurable: true,
      value: "dist/assets/app.js",
    });

    await userEvent.upload(within(dialog).getByLabelText("Folder"), [
      indexFile,
      appFile,
    ]);
    expect(within(dialog).getByText("docs/dist/")).toBeInTheDocument();
    await userEvent.type(
      within(dialog).getByLabelText("Domains"),
      "demo.localhost",
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Upload and publish" }),
    );

    await waitFor(() => {
      expect(uploadAndPublishSite).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "demo",
          parentPrefix: "docs/",
          files: [indexFile, appFile],
          domains: ["demo.localhost"],
          enabled: true,
          indexDocument: "index.html",
          errorDocument: "",
          spaFallback: true,
          onProgress: expect.any(Function),
        },
      );
    });
    expect(checkObjectExists).not.toHaveBeenCalled();

    expect(await screen.findByText("Site published")).toBeInTheDocument();
  });

  it("uploads a file and publishes a site from the toolbar", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({ items: [], next_cursor: "" })
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "docs/landing.html",
            name: "landing.html",
            is_empty: null,
            object_key: "docs/landing.html",
            original_filename: "landing.html",
            size: 18,
            content_type: "text/html",
            etag: "feedface12345678",
            visibility: "public",
            updated_at: "2026-03-25T00:02:00Z",
          },
        ],
        next_cursor: "",
      });
    vi.mocked(uploadFileAndPublishSite).mockResolvedValue({
      id: 9,
      bucket: "demo",
      root_prefix: "docs/",
      enabled: true,
      index_document: "landing.html",
      error_document: "",
      spa_fallback: true,
      domains: ["demo.localhost"],
      created_at: "2026-03-30T00:00:00Z",
      updated_at: "2026-03-30T00:00:00Z",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload and publish" }),
    );

    const dialog = await screen.findByRole("dialog");
    await userEvent.click(
      within(dialog).getByRole("tab", { name: "Upload file and publish" }),
    );

    const landingFile = new File(["<html>home</html>"], "landing.html", {
      type: "text/html",
    });
    await userEvent.upload(within(dialog).getByLabelText("File"), landingFile);
    expect(within(dialog).getAllByText("docs/")).toHaveLength(2);
    expect(within(dialog).getByText("landing.html")).toBeInTheDocument();
    await userEvent.type(
      within(dialog).getByLabelText("Domains"),
      "demo.localhost",
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Upload and publish" }),
    );

    await waitFor(() => {
      expect(uploadFileAndPublishSite).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "demo",
          parentPrefix: "docs/",
          file: landingFile,
          domains: ["demo.localhost"],
          enabled: true,
          errorDocument: "",
          spaFallback: true,
          onProgress: expect.any(Function),
        },
      );
    });

    expect(await screen.findByText("Site published")).toBeInTheDocument();
  });

  it("shows an error toast when folder upload fails", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(uploadFolder).mockRejectedValue(
      new Error("folder upload failed"),
    );

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload folder" }),
    );

    const readme = new File(["hello"], "readme.txt", { type: "text/plain" });
    Object.defineProperty(readme, "webkitRelativePath", {
      configurable: true,
      value: "assets/readme.txt",
    });

    await userEvent.upload(await screen.findByLabelText("Folder"), readme);
    await userEvent.click(
      screen.getByRole("button", { name: "Start folder upload" }),
    );

    expect(await screen.findByText("folder upload failed")).toBeInTheDocument();
  });

  it("navigates explorer back and forward by prefix history", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo?prefix=docs/assets/" },
    );

    const backButton = await screen.findByRole("button", { name: "Go back" });
    expect(backButton).toBeEnabled();
    expect(
      screen.getByRole("button", { name: "Go forward" }),
    ).toBeDisabled();

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          prefix: "docs/assets/",
        }),
      );
    });

    await userEvent.click(backButton);

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          prefix: "docs/",
        }),
      );
    });

    expect(screen.queryByText("assets")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Go forward" })).toBeEnabled();

    await userEvent.click(screen.getByRole("button", { name: "Go forward" }));

    await waitFor(() => {
      expect(listExplorerEntries).toHaveBeenLastCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        expect.objectContaining({
          bucket: "demo",
          prefix: "docs/assets/",
        }),
      );
    });
  });

  it("creates a folder from the toolbar dialog", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [],
      next_cursor: "",
    });
    vi.mocked(createFolder).mockResolvedValue({
      path: "assets/",
      name: "assets",
      parent_path: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "New folder" }),
    );
    await userEvent.type(await screen.findByLabelText("Folder name"), "assets");
    await userEvent.click(
      screen.getByRole("button", { name: "Create folder" }),
    );

    await waitFor(() => {
      expect(createFolder).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "demo",
          prefix: "",
          name: "assets",
        },
      );
    });
  });

  it("confirms file deletion before removing an object", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "file",
          path: "docs/readme.txt",
          name: "readme.txt",
          is_empty: null,
          object_key: "docs/readme.txt",
          original_filename: "readme.txt",
          size: 12,
          content_type: "text/plain",
          etag: "abcdef1234567890",
          visibility: "public",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
      next_cursor: "",
    });
    vi.mocked(deleteObject).mockResolvedValue(undefined);

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    const fileRow = await screen.findByRole("row", { name: /readme\.txt/ });
    await userEvent.click(
      within(fileRow).getByRole("button", { name: "More actions" }),
    );
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Delete" }),
    );

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Delete object?")).toBeInTheDocument();

    await userEvent.click(
      within(dialog).getByRole("button", { name: "Delete" }),
    );

    await waitFor(() => {
      expect(deleteObject).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        "demo",
        "docs/readme.txt",
      );
    });
  });

  it("supports recursive folder deletion from the table", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "directory",
          path: "docs/",
          name: "docs",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
      ],
      next_cursor: "",
    });
    vi.mocked(deleteFolder).mockResolvedValue(undefined);

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    const folderRow = await screen.findByRole("row", { name: /docs/ });
    await userEvent.click(
      within(folderRow).getByRole("button", { name: "More actions" }),
    );
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Delete folder" }),
    );

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Delete folder?")).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        "This removes the folder docs from demo together with all nested files and folders.",
      ),
    ).toBeInTheDocument();

    await userEvent.click(
      within(dialog).getByRole("button", { name: "Delete" }),
    );

    await waitFor(() => {
      expect(deleteFolder).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        "demo",
        "docs/",
        { recursive: true },
      );
    });
  });

  it("shows publish site for both directory and file rows", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "directory",
          path: "docs/",
          name: "docs",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
        {
          type: "file",
          path: "docs/readme.txt",
          name: "readme.txt",
          is_empty: null,
          object_key: "docs/readme.txt",
          original_filename: "readme.txt",
          size: 12,
          content_type: "text/plain",
          etag: "abcdef1234567890",
          visibility: "public",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
      next_cursor: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    expect(
      await screen.findAllByRole("button", { name: "Publish site" }),
    ).toHaveLength(2);
    expect(
      screen.queryByRole("button", { name: "Delete" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getAllByRole("button", { name: "More actions" }),
    ).toHaveLength(2);
  });

  it("publishes a folder as a site from the explorer table", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "directory",
          path: "docs/",
          name: "docs",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
      ],
      next_cursor: "",
    });
    vi.mocked(createSite).mockResolvedValue({
      id: 1,
      bucket: "demo",
      root_prefix: "docs/",
      enabled: true,
      index_document: "index.html",
      error_document: "",
      spa_fallback: true,
      domains: ["demo.localhost", "www.localhost"],
      created_at: "2026-03-30T00:00:00Z",
      updated_at: "2026-03-30T00:00:00Z",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Publish site" }),
    );

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("demo")).toBeInTheDocument();
    expect(within(dialog).getByText("docs/")).toBeInTheDocument();

    await userEvent.type(
      within(dialog).getByLabelText("Domains"),
      "demo.localhost, www.localhost",
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Publish site" }),
    );

    await waitFor(() => {
      expect(createSite).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "demo",
          root_prefix: "docs/",
          enabled: true,
          index_document: "index.html",
          error_document: "",
          spa_fallback: true,
          domains: ["demo.localhost", "www.localhost"],
        },
      );
    });

    expect(await screen.findByText("Site published")).toBeInTheDocument();
  });

  it("publishes a file as a site from the explorer table", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "docs/readme.txt",
            name: "readme.txt",
            is_empty: null,
            object_key: "docs/readme.txt",
            original_filename: "readme.txt",
            size: 12,
            content_type: "text/plain",
            etag: "abcdef1234567890",
            visibility: "private",
            updated_at: "2026-03-25T00:00:00Z",
          },
        ],
        next_cursor: "",
      })
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "docs/readme.txt",
            name: "readme.txt",
            is_empty: null,
            object_key: "docs/readme.txt",
            original_filename: "readme.txt",
            size: 12,
            content_type: "text/plain",
            etag: "abcdef1234567890",
            visibility: "public",
            updated_at: "2026-03-25T00:00:00Z",
          },
        ],
        next_cursor: "",
      });
    vi.mocked(publishObjectSite).mockResolvedValue({
      id: 9,
      bucket: "demo",
      root_prefix: "docs/",
      enabled: true,
      index_document: "readme.txt",
      error_document: "",
      spa_fallback: true,
      domains: ["demo.localhost"],
      created_at: "2026-03-30T00:00:00Z",
      updated_at: "2026-03-30T00:00:00Z",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Publish site" }),
    );

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("demo")).toBeInTheDocument();
    expect(within(dialog).getByText("docs/")).toBeInTheDocument();
    expect(within(dialog).getByText("readme.txt")).toBeInTheDocument();

    await userEvent.type(
      within(dialog).getByLabelText("Domains"),
      "demo.localhost",
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Publish site" }),
    );

    await waitFor(() => {
      expect(publishObjectSite).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "demo",
          objectKey: "docs/readme.txt",
          domains: ["demo.localhost"],
          enabled: true,
          errorDocument: "",
          spaFallback: true,
        },
      );
    });

    expect(await screen.findByText("Site published")).toBeInTheDocument();
  });

  it("shows a site publish error toast when the request fails", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "directory",
          path: "docs/",
          name: "docs",
          is_empty: false,
          object_key: null,
          original_filename: null,
          size: null,
          content_type: null,
          etag: null,
          visibility: null,
          updated_at: null,
        },
      ],
      next_cursor: "",
    });
    vi.mocked(createSite).mockRejectedValue(new Error("publish failed"));

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Publish site" }),
    );

    const dialog = await screen.findByRole("dialog");
    await userEvent.type(
      within(dialog).getByLabelText("Domains"),
      "demo.localhost",
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Publish site" }),
    );

    expect(await screen.findByText("publish failed")).toBeInTheDocument();
  });

  it("shows a success toast after copying a public URL", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "file",
          path: "docs/readme.txt",
          name: "readme.txt",
          is_empty: null,
          object_key: "docs/readme.txt",
          original_filename: "readme.txt",
          size: 12,
          content_type: "text/plain",
          etag: "abcdef1234567890",
          visibility: "public",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
      next_cursor: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "Copy URL" }),
    );

    await waitFor(() => {
      expect(window.navigator.clipboard.writeText).toHaveBeenCalledWith(
        "http://localhost:8080/download",
      );
    });

    expect(await screen.findByText("URL copied")).toBeInTheDocument();
  });

  it("opens a file details dialog from the actions column", async () => {
    vi.mocked(listExplorerEntries).mockResolvedValue({
      items: [
        {
          type: "file",
          path: "images/avatar.png",
          name: "avatar.png",
          is_empty: null,
          object_key: "images/avatar.png",
          original_filename: "avatar.png",
          size: 4096,
          content_type: "image/png",
          etag: "abc123",
          visibility: "public",
          updated_at: "2026-03-25T00:00:00Z",
        },
      ],
      next_cursor: "",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "View details" }),
    );

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("File details")).toBeInTheDocument();
    expect(within(dialog).getByText("avatar.png")).toBeInTheDocument();
    expect(within(dialog).getByText("image/png")).toBeInTheDocument();
    expect(within(dialog).getByAltText("file preview")).toHaveAttribute(
      "src",
      "http://localhost:8080/download",
    );
  });

  it("updates visibility from file details and refreshes entries", async () => {
    vi.mocked(listExplorerEntries)
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "docs/readme.txt",
            name: "readme.txt",
            is_empty: null,
            object_key: "docs/readme.txt",
            original_filename: "readme.txt",
            size: 12,
            content_type: "text/plain",
            etag: "abcdef1234567890",
            visibility: "private",
            updated_at: "2026-03-25T00:00:00Z",
          },
        ],
        next_cursor: "",
      })
      .mockResolvedValueOnce({
        items: [
          {
            type: "file",
            path: "docs/readme.txt",
            name: "readme.txt",
            is_empty: null,
            object_key: "docs/readme.txt",
            original_filename: "readme.txt",
            size: 12,
            content_type: "text/plain",
            etag: "abcdef1234567890",
            visibility: "public",
            updated_at: "2026-03-25T00:00:00Z",
          },
        ],
        next_cursor: "",
      });

    vi.mocked(updateObjectVisibility).mockResolvedValue({
      id: 1,
      bucket_name: "demo",
      object_key: "docs/readme.txt",
      original_filename: "readme.txt",
      size: 12,
      content_type: "text/plain",
      etag: "abcdef1234567890",
      visibility: "public",
      created_at: "2026-03-25T00:00:00Z",
      updated_at: "2026-03-25T00:00:00Z",
    });

    renderWithApp(
      <Routes>
        <Route path="/buckets/:bucket" element={<BucketObjectsPage />} />
      </Routes>,
      { route: "/buckets/demo" },
    );

    await userEvent.click(
      await screen.findByRole("button", { name: "View details" }),
    );

    await userEvent.click(
      await screen.findByRole("combobox", { name: "Visibility" }),
    );
    await userEvent.click(
      await screen.findByRole("option", { name: "Public" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(updateObjectVisibility).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "demo",
          objectKey: "docs/readme.txt",
          visibility: "public",
        },
      );
    });

    expect(
      await screen.findByText("Object visibility updated"),
    ).toBeInTheDocument();
  });
});

function createRecycleBinItem(
  overrides: Partial<{
    id: number;
    type: "file" | "directory";
    bucket_name: string;
    path: string;
    name: string;
    object_key: string;
    original_filename: string;
    size: number;
    content_type: string;
    etag: string;
    visibility: "public" | "private";
    created_at: string;
    deleted_at: string;
  }> = {},
) {
  return {
    id: 101,
    type: "file" as const,
    bucket_name: "demo",
    path: "docs/report.txt",
    name: "report.txt",
    object_key: "docs/report.txt",
    original_filename: "report.txt",
    size: 128,
    content_type: "text/plain",
    etag: "etag-1",
    visibility: "private" as const,
    created_at: "2026-04-20T00:00:00Z",
    deleted_at: "2026-04-21T00:00:00Z",
    ...overrides,
  };
}
