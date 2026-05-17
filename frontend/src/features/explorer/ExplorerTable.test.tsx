import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import type {
  ExplorerDirectoryEntry,
  ExplorerEntry,
  ExplorerFileEntry,
} from "../../api/types";
import type { ExplorerSortBy, ExplorerSortOrder } from "../../lib/explorer";
import { renderWithApp } from "../../test/test-utils";
import { ExplorerTable } from "./ExplorerTable";

const sonner = vi.hoisted(() => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("sonner", () => ({
  toast: sonner.toast,
  Toaster: () => null,
}));

describe("ExplorerTable", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

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
    sonner.toast.success.mockReset();
    sonner.toast.error.mockReset();
  });

  it("shows sort popover actions and applies sorting only after confirmation", async () => {
    const onSortApply = vi.fn();

    renderExplorerTable(createFileEntry({}), {
      onSortApply,
    });

    await userEvent.click(
      screen.getByRole("button", { name: "Sort Size: not sorted" }),
    );

    const title = await screen.findByText("Sort by Size");
    const popover = title.closest(
      "[data-slot='popover-content']",
    ) as HTMLElement | null;

    expect(popover).not.toBeNull();
    expect(
      within(popover!).getByText("Choose an order and confirm to apply it."),
    ).toBeInTheDocument();
    expect(
      within(popover!).getByRole("radio", { name: "Ascending" }),
    ).not.toBeChecked();
    expect(
      within(popover!).getByRole("radio", { name: "Descending" }),
    ).not.toBeChecked();

    await userEvent.click(within(popover!).getByRole("button", { name: "Apply" }));
    expect(sonner.toast.error).toHaveBeenCalledWith(
      "Select a sort order before applying.",
    );
    expect(onSortApply).not.toHaveBeenCalled();
    expect(screen.getByText("Sort by Size")).toBeInTheDocument();

    await userEvent.click(
      within(popover!).getByRole("radio", { name: "Descending" }),
    );
    expect(onSortApply).not.toHaveBeenCalled();

    await userEvent.click(
      within(popover!).getByRole("button", { name: "Apply" }),
    );
    expect(onSortApply).toHaveBeenCalledWith("size", "desc");
  });

  it("exposes a clear action for the active sort", async () => {
    const onSortClear = vi.fn();

    renderExplorerTable(createFileEntry({}), {
      onSortClear,
      sortBy: "size",
      sortOrder: "desc",
    });

    await userEvent.click(
      screen.getByRole("button", { name: "Sort Size: descending" }),
    );

    const title = await screen.findByText("Sort by Size");
    const popover = title.closest(
      "[data-slot='popover-content']",
    ) as HTMLElement | null;

    expect(popover).not.toBeNull();

    await userEvent.click(
      within(popover!).getByRole("button", { name: "Clear sorting" }),
    );
    expect(onSortClear).toHaveBeenCalledTimes(1);
  });

  it("toggles row selection with checkboxes", async () => {
    renderExplorerTable(createFileEntry({}));

    const rowCheckbox = screen.getByRole("checkbox", {
      name: "Select file.txt",
    });
    const row = rowCheckbox.closest("tr");

    expect(row).not.toHaveAttribute("data-state", "selected");

    await userEvent.click(rowCheckbox);

    expect(row).toHaveAttribute("data-state", "selected");

    await userEvent.click(rowCheckbox);

    expect(row).not.toHaveAttribute("data-state", "selected");
  });

  it("allows selecting entry names as text", () => {
    renderExplorerTable(createFileEntry({}));

    expect(screen.getByRole("button", { name: "file.txt" })).toHaveClass(
      "select-text",
    );
  });

  it("supports select all and indeterminate selection states", async () => {
    renderExplorerTable([
      createFileEntry({
        name: "alpha.txt",
        object_key: "docs/alpha.txt",
        original_filename: "alpha.txt",
        path: "docs/alpha.txt",
      }),
      createDirectoryEntry({
        name: "assets",
        path: "assets/",
      }),
    ]);

    const selectAllCheckbox = screen.getByRole("checkbox", {
      name: "Select all items",
    });
    const firstRowCheckbox = screen.getByRole("checkbox", {
      name: "Select alpha.txt",
    });
    const secondRowCheckbox = screen.getByRole("checkbox", {
      name: "Select assets",
    });

    await userEvent.click(firstRowCheckbox);

    expect(selectAllCheckbox).toHaveAttribute("data-state", "indeterminate");

    await userEvent.click(selectAllCheckbox);

    expect(selectAllCheckbox).toHaveAttribute("data-state", "checked");
    expect(firstRowCheckbox.closest("tr")).toHaveAttribute(
      "data-state",
      "selected",
    );
    expect(secondRowCheckbox.closest("tr")).toHaveAttribute(
      "data-state",
      "selected",
    );

    await userEvent.click(selectAllCheckbox);

    expect(selectAllCheckbox).toHaveAttribute("data-state", "unchecked");
    expect(firstRowCheckbox.closest("tr")).not.toHaveAttribute(
      "data-state",
      "selected",
    );
    expect(secondRowCheckbox.closest("tr")).not.toHaveAttribute(
      "data-state",
      "selected",
    );
  });

  it("keeps the header table separate from the scrollable body area", () => {
    renderExplorerTable(createFileEntry({}));

    const tables = screen.getAllByRole("table");
    const scrollContainer = screen.getByTestId("explorer-table-scroll-container");

    expect(tables).toHaveLength(2);
    expect(scrollContainer.className).toContain("flex-1");
    expect(scrollContainer.className).toContain("overflow-auto");
    expect(scrollContainer).toContainElement(tables[1]);
    expect(scrollContainer).not.toContainElement(tables[0]);
  });

  it("virtualizes large result sets instead of rendering every row at once", () => {
    renderExplorerTable(createManyFileEntries(120));

    const rowCheckboxes = screen
      .getAllByRole("checkbox")
      .filter((checkbox) => checkbox.getAttribute("aria-label") !== "Select all items");

    expect(rowCheckboxes.length).toBeLessThan(40);
    expect(
      screen.queryByRole("button", { name: "file-119.txt" }),
    ).not.toBeInTheDocument();
  });

  it("renders later rows after scrolling and keeps row actions interactive", async () => {
    renderExplorerTable(createManyFileEntries(120));

    const scrollContainer = screen.getByTestId("explorer-table-scroll-container");
    Object.defineProperty(scrollContainer, "scrollTop", {
      configurable: true,
      value: 49 * 100,
      writable: true,
    });

    fireEvent.scroll(scrollContainer);

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "file-110.txt" }),
      ).toBeInTheDocument();
    });

    await userEvent.click(screen.getByRole("button", { name: "file-110.txt" }));

    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByText((_, element) => {
        return (
          element?.tagName === "DD" &&
          element.textContent === "file-110.txt"
        );
      }),
    ).toBeInTheDocument();
  });

  it("routes public and private file download actions through the shared callback", async () => {
    const onDownloadFile = vi.fn().mockResolvedValue(undefined);

    renderExplorerTable(
      [
        createFileEntry({
          name: "public.txt",
          object_key: "docs/public.txt",
          original_filename: "public.txt",
          path: "docs/public.txt",
          visibility: "public",
        }),
        createFileEntry({
          name: "private.txt",
          object_key: "docs/private.txt",
          original_filename: "private.txt",
          path: "docs/private.txt",
          visibility: "private",
        }),
      ],
      {
        onDownloadFile,
      },
    );

    await userEvent.click(
      screen.getByRole("button", { name: "Direct download" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Signed download" }),
    );

    expect(onDownloadFile).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({
        object_key: "docs/public.txt",
        path: "docs/public.txt",
        visibility: "public",
      }),
    );
    expect(onDownloadFile).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        object_key: "docs/private.txt",
        path: "docs/private.txt",
        visibility: "private",
      }),
    );
  });

  it("keeps file row actions within three visible actions plus an overflow menu", async () => {
    renderExplorerTable(createFileEntry({}));

    expect(
      screen.getByRole("button", { name: "View details" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Direct download" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Publish site" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "More actions" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Delete" }),
    ).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "More actions" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Delete" }));

    expect(await screen.findByText("Delete object?")).toBeInTheDocument();
  });

  it("keeps directory row actions within three visible actions plus an overflow menu", async () => {
    renderExplorerTable(createDirectoryEntry({}));

    expect(
      screen.getByRole("button", { name: "Open folder" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Download ZIP" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Publish site" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "More actions" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Delete folder" }),
    ).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "More actions" }));
    await userEvent.click(
      screen.getByRole("menuitem", { name: "Delete folder" }),
    );

    expect(await screen.findByText("Delete folder?")).toBeInTheDocument();
  });

  it("applies wrap-anywhere classes to long file names in details dialogs", async () => {
    const longFileName =
      "averyveryveryveryveryveryveryveryveryveryveryverylongfilenamewithoutspaces.txt";

    renderExplorerTable(
      createFileEntry({
        content_type: "text/plain",
        name: longFileName,
        object_key: `docs/${longFileName}`,
        original_filename: longFileName,
        path: `docs/${longFileName}`,
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: longFileName }));

    const dialog = await screen.findByRole("dialog");
    const title = within(dialog).getByRole("heading", { name: "File details" });
    const originalFilenameValue = within(dialog).getByText((_, element) => {
      return element?.tagName === "DD" && element.textContent === longFileName;
    });

    expect(title.className).toContain("wrap-anywhere");
    expect(originalFilenameValue.className).toContain(
      "wrap-anywhere",
    );
  });

  it("uses shared wrap-anywhere classes for long object keys in delete dialogs", async () => {
    const longObjectKey =
      "docs/averyveryveryveryveryveryveryveryveryveryveryveryveryveryveryverylongobjectkey.txt";

    renderExplorerTable(
      createFileEntry({
        object_key: longObjectKey,
        path: longObjectKey,
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "More actions" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "Delete" }));

    const dialog = await screen.findByRole("alertdialog");
    const description = within(dialog).getByText((_, element) => {
      return (
        element?.getAttribute("data-slot") === "alert-dialog-description" &&
        element.textContent?.includes(longObjectKey) === true
      );
    });

    expect(description.className).toContain("wrap-anywhere");
  });

  it("renders markdown previews as markdown content", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: vi
        .fn()
        .mockResolvedValue(
          '# Hello\n\n- item one\n\n```ts\nconst answer = 42;\n```\n\n<div data-testid="raw-html">unsafe</div>',
        ),
    });
    vi.stubGlobal("fetch", fetchMock);

    renderExplorerTable(
      createFileEntry({
        content_type: "text/markdown",
        name: "test.md",
        object_key: "docs/test.md",
        original_filename: "test.md",
        path: "docs/test.md",
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "test.md" }));

    const dialog = await screen.findByRole("dialog");

    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.localhost/api/v1/buckets/demo/objects/docs/test.md",
      expect.objectContaining({
        signal: expect.any(AbortSignal),
      }),
    );
    expect(
      await within(dialog).findByRole("heading", { level: 1, name: "Hello" }),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("item one")).toBeInTheDocument();
    expect(within(dialog).getByText("const answer = 42;")).toBeInTheDocument();
    expect(within(dialog).queryByText("unsafe")).not.toBeInTheDocument();
    expect(within(dialog).queryByTitle("file preview")).not.toBeInTheDocument();
  });

  it("treats markdown filenames as markdown even with legacy content types", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: vi.fn().mockResolvedValue("# Legacy Markdown"),
    });
    vi.stubGlobal("fetch", fetchMock);

    renderExplorerTable(
      createFileEntry({
        content_type: "application/octet-stream",
        name: "README.md",
        object_key: "docs/README.md",
        original_filename: "README.md",
        path: "docs/README.md",
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "README.md" }));

    const dialog = await screen.findByRole("dialog");

    expect(
      await within(dialog).findByRole("heading", {
        level: 1,
        name: "Legacy Markdown",
      }),
    ).toBeInTheDocument();
  });

  it("does not preview markdown files larger than 100 KB", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    renderExplorerTable(
      createFileEntry({
        content_type: "text/markdown",
        name: "large.md",
        object_key: "docs/large.md",
        original_filename: "large.md",
        path: "docs/large.md",
        size: 100 * 1024 + 1,
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "large.md" }));

    const dialog = await screen.findByRole("dialog");

    expect(fetchMock).not.toHaveBeenCalled();
    expect(
      within(dialog).getByText("This file is too large to preview. Markdown preview is not supported above 100 KB."),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("button", { name: "Fullscreen preview" }),
    ).not.toBeInTheDocument();
  });

  it("keeps plain text previews as raw text", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      text: vi.fn().mockResolvedValue("# Plain heading\n- item"),
    });
    vi.stubGlobal("fetch", fetchMock);

    renderExplorerTable(
      createFileEntry({
        content_type: "text/plain",
        name: "notes.txt",
        object_key: "docs/notes.txt",
        original_filename: "notes.txt",
        path: "docs/notes.txt",
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "notes.txt" }));

    const dialog = await screen.findByRole("dialog");
    const pre = within(dialog).getByText((_, element) => {
      return (
        element?.tagName === "PRE" &&
        element.textContent === "# Plain heading\n- item"
      );
    });

    expect(pre).toBeInTheDocument();
    expect(
      within(dialog).queryByText("item", { selector: "li" }),
    ).not.toBeInTheDocument();
  });

  it("does not preview txt files larger than 100 KB", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    renderExplorerTable(
      createFileEntry({
        content_type: "text/plain",
        name: "large.txt",
        object_key: "docs/large.txt",
        original_filename: "large.txt",
        path: "docs/large.txt",
        size: 100 * 1024 + 1,
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "large.txt" }));

    const dialog = await screen.findByRole("dialog");

    expect(fetchMock).not.toHaveBeenCalled();
    expect(
      within(dialog).getByText(
        "This file is too large to preview. Text preview is not supported above 100 KB.",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("button", { name: "Fullscreen preview" }),
    ).not.toBeInTheDocument();
  });

  it("does not preview other text files larger than 100 KB", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    renderExplorerTable(
      createFileEntry({
        content_type: "application/json",
        name: "large.json",
        object_key: "docs/large.json",
        original_filename: "large.json",
        path: "docs/large.json",
        size: 100 * 1024 + 1,
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "large.json" }));

    const dialog = await screen.findByRole("dialog");

    expect(fetchMock).not.toHaveBeenCalled();
    expect(
      within(dialog).getByText(
        "This file is too large to preview. Text preview is not supported above 100 KB.",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("button", { name: "Fullscreen preview" }),
    ).not.toBeInTheDocument();
  });

  it("opens image previews in a fullscreen dialog", async () => {
    renderExplorerTable(
      createFileEntry({
        content_type: "image/png",
        name: "avatar.png",
        object_key: "images/avatar.png",
        original_filename: "avatar.png",
        path: "images/avatar.png",
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "avatar.png" }));

    const detailsDialog = await screen.findByRole("dialog");
    const inlinePreviewSurface = within(detailsDialog).getByTestId(
      "inline-preview-surface",
    );

    expect(
      within(inlinePreviewSurface).getByRole("button", {
        name: "Fullscreen preview",
      }),
    ).toBeInTheDocument();

    await userEvent.click(
      within(inlinePreviewSurface).getByRole("button", {
        name: "Fullscreen preview",
      }),
    );

    const fullscreenDialog = await screen.findByRole("dialog", {
      name: "avatar.png",
    });

    expect(
      within(fullscreenDialog).getByText("avatar.png"),
    ).toBeInTheDocument();
    expect(
      within(fullscreenDialog).getByAltText("file preview"),
    ).toHaveAttribute(
      "src",
      "https://api.localhost/api/v1/buckets/demo/objects/images/avatar.png",
    );
  });

  it("uses embedded PDF preview parameters to avoid native viewer overflow", async () => {
    renderExplorerTable(
      createFileEntry({
        content_type: "application/pdf",
        name: "test.pdf",
        object_key: "docs/test.pdf",
        original_filename: "test.pdf",
        path: "docs/test.pdf",
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "test.pdf" }));

    const dialog = await screen.findByRole("dialog");

    expect(within(dialog).getByTitle("file preview")).toHaveAttribute(
      "src",
      "https://api.localhost/api/v1/buckets/demo/objects/docs/test.pdf#toolbar=0&navpanes=0&pagemode=none&view=Fit&zoom=page-fit",
    );
  });

  it.each([
    {
      contentType:
        "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
      name: "report.docx",
    },
    {
      contentType:
        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      name: "budget.xlsx",
    },
    {
      contentType:
        "application/vnd.openxmlformats-officedocument.presentationml.presentation",
      name: "deck.pptx",
    },
  ])(
    "does not preview OpenXML office files: $name",
    async ({ contentType, name }) => {
      const fetchMock = vi.fn();
      vi.stubGlobal("fetch", fetchMock);

      renderExplorerTable(
        createFileEntry({
          content_type: contentType,
          name,
          object_key: `docs/${name}`,
          original_filename: name,
          path: `docs/${name}`,
        }),
      );

      await userEvent.click(screen.getByRole("button", { name }));

      const dialog = await screen.findByRole("dialog");

      expect(fetchMock).not.toHaveBeenCalled();
      expect(within(dialog).getByText("Not available")).toBeInTheDocument();
      expect(dialog.querySelector('iframe[title="file preview"]')).toBeNull();
      expect(dialog.querySelector("pre")).toBeNull();
    },
  );

  it.each([
    {
      contentType: "application/xml",
      name: "feed.xml",
    },
    {
      contentType: "application/atom+xml",
      name: "atom.xml",
    },
  ])(
    "keeps real XML content previewable as text: $contentType",
    async ({ contentType, name }) => {
      const fetchMock = vi.fn().mockResolvedValue({
        ok: true,
        text: vi.fn().mockResolvedValue("<root><item>value</item></root>"),
      });
      vi.stubGlobal("fetch", fetchMock);

      renderExplorerTable(
        createFileEntry({
          content_type: contentType,
          name,
          object_key: `docs/${name}`,
          original_filename: name,
          path: `docs/${name}`,
        }),
      );

      await userEvent.click(screen.getByRole("button", { name }));

      const dialog = await screen.findByRole("dialog");
      const pre = within(dialog).getByText((_, element) => {
        return (
          element?.tagName === "PRE" &&
          element.textContent === "<root><item>value</item></root>"
        );
      });

      expect(fetchMock).toHaveBeenCalledWith(
        `https://api.localhost/api/v1/buckets/demo/objects/docs/${name}`,
        expect.objectContaining({
          signal: expect.any(AbortSignal),
        }),
      );
      expect(pre).toBeInTheDocument();
    },
  );
});

function createFileEntry(
  overrides: Partial<ExplorerFileEntry>,
): ExplorerFileEntry {
  return {
    type: "file",
    path: "docs/file.txt",
    name: "file.txt",
    is_empty: null,
    object_key: "docs/file.txt",
    original_filename: "file.txt",
    size: 7,
    content_type: "text/plain",
    etag: "etag",
    visibility: "public",
    updated_at: "2026-04-07T01:00:00Z",
    ...overrides,
  };
}

function createDirectoryEntry(
  overrides: Partial<ExplorerDirectoryEntry>,
): ExplorerDirectoryEntry {
  return {
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
    created_at: null,
    updated_at: null,
    ...overrides,
  };
}

function createManyFileEntries(count: number): ExplorerFileEntry[] {
  return Array.from({ length: count }, (_, index) =>
    createFileEntry({
      name: `file-${index}.txt`,
      object_key: `docs/file-${index}.txt`,
      original_filename: `file-${index}.txt`,
      path: `docs/file-${index}.txt`,
    }),
  );
}

function renderExplorerTable(
  entry: ExplorerEntry | ExplorerEntry[],
  options?: {
    onDownloadFile?: (entry: ExplorerFileEntry) => Promise<void>;
    onSortApply?: (
      sortBy: ExplorerSortBy,
      sortOrder: ExplorerSortOrder,
    ) => void;
    onSortClear?: () => void;
    sortBy?: ExplorerSortBy | null;
    sortOrder?: ExplorerSortOrder | null;
  },
) {
  const entries = Array.isArray(entry) ? entry : [entry];

  renderWithApp(<ExplorerTableHarness entries={entries} options={options} />);
}

function ExplorerTableHarness({
  entries,
  options,
}: {
  entries: ExplorerEntry[];
  options?: {
    onDownloadFile?: (entry: ExplorerFileEntry) => Promise<void>;
    onSortApply?: (
      sortBy: ExplorerSortBy,
      sortOrder: ExplorerSortOrder,
    ) => void;
    onSortClear?: () => void;
    sortBy?: ExplorerSortBy | null;
    sortOrder?: ExplorerSortOrder | null;
  };
}) {
  const [selectedPaths, setSelectedPaths] = useState<Set<string>>(
    () => new Set(),
  );

  return (
    <ExplorerTable
      bucket="demo"
      buildPublicUrl={(objectKey) =>
        `https://api.localhost/api/v1/buckets/demo/objects/${objectKey}`
      }
      deletingPath=""
      downloadingFilePath=""
      downloadingFolderPath=""
      entries={entries}
      onDeleteFile={vi.fn().mockResolvedValue(undefined)}
      onDeleteFolder={vi.fn().mockResolvedValue(undefined)}
      onDownloadFile={
        options?.onDownloadFile ?? vi.fn().mockResolvedValue(undefined)
      }
      onDownloadFolder={vi.fn().mockResolvedValue(undefined)}
      onOpenDirectory={vi.fn()}
      onPublishObjectSite={vi.fn().mockResolvedValue(undefined)}
      onPublishSite={vi.fn().mockResolvedValue(undefined)}
      onSelectAll={(checked) => {
        setSelectedPaths(
          checked === true
            ? new Set(entries.map((entry) => entry.path))
            : new Set(),
        );
      }}
      onSelectEntry={(entryPath, checked) => {
        setSelectedPaths((current) => {
          const next = new Set(current);

          if (checked === true) {
            next.add(entryPath);
          } else {
            next.delete(entryPath);
          }

          return next;
        });
      }}
      onSortApply={options?.onSortApply ?? vi.fn()}
      onSortClear={options?.onSortClear ?? vi.fn()}
      onUpdateVisibility={vi.fn().mockResolvedValue(undefined)}
      publishingPath=""
      selectedPaths={selectedPaths}
      sortBy={options?.sortBy ?? null}
      sortOrder={options?.sortOrder ?? null}
    />
  );
}
