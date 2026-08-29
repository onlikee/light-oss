import {
  CopyIcon,
  DownloadIcon,
  EyeIcon,
  FileDirectoryFillIcon,
  FileDirectoryOpenFillIcon,
  FileIcon,
  GlobeIcon,
  KebabHorizontalIcon,
  LockIcon,
  ScreenFullIcon,
  ShieldIcon,
  FilterIcon,
  SortAscIcon,
  SortDescIcon,
  SyncIcon,
  TrashIcon,
  UnlockIcon,
} from "@primer/octicons-react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { forwardRef, useEffect, useLayoutEffect, useState } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { toast } from "sonner";
import type {
  ExplorerDirectoryEntry,
  ExplorerEntry,
  ExplorerFileEntry,
  ObjectVisibility,
} from "@/api/types";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { formatBytes, formatDate } from "@/lib/format";
import type { ExplorerSortBy, ExplorerSortOrder } from "@/lib/explorer";
import { useI18n } from "@/lib/i18n";
import {
  PublishObjectSiteDialog,
  type PublishObjectSiteValue,
} from "@/features/explorer/PublishObjectSiteDialog";
import {
  PublishSiteDialog,
  type PublishSiteValue,
} from "@/features/explorer/PublishSiteDialog";
import type { AppLocale } from "@/lib/preferences";
import { cn } from "@/lib/utils";

const explorerTableMinWidthClass = "min-w-[85.5rem]";
const explorerSelectionColumnWidthClass = "w-6";
const explorerHeaderCellClass =
  "bg-card shadow-[inset_0_-1px_0_var(--color-border)]";
const explorerTableColumnCount = 7;
const explorerTableRowEstimate = 49;
const explorerTableOverscan = 10;

export function ExplorerTable({
  bucket,
  buildPublicUrl,
  deletingPath,
  downloadingFilePath,
  downloadingFolderPath,
  entries,
  onDeleteFile,
  onDeleteFolder,
  onDownloadFile,
  onDownloadFolder,
  onOpenDirectory,
  onPublishObjectSite,
  onPublishSite,
  onSelectAll,
  onSelectEntry,
  onSortApply,
  onSortClear,
  onUpdateVisibility,
  publishingPath,
  selectedPaths,
  selectionDisabled = false,
  sortBy,
  sortOrder,
}: {
  bucket: string;
  buildPublicUrl: (objectKey: string) => string;
  deletingPath: string;
  downloadingFilePath: string;
  downloadingFolderPath: string;
  entries: ExplorerEntry[];
  onDeleteFile: (objectKey: string) => Promise<void>;
  onDeleteFolder: (folderPath: string) => Promise<void>;
  onDownloadFile: (entry: ExplorerFileEntry) => Promise<void>;
  onDownloadFolder: (folderPath: string) => Promise<void>;
  onOpenDirectory: (folderPath: string) => void;
  onPublishObjectSite: (
    objectKey: string,
    value: PublishObjectSiteValue,
  ) => Promise<void>;
  onPublishSite: (folderPath: string, value: PublishSiteValue) => Promise<void>;
  onSelectAll: (checked: boolean | "indeterminate") => void;
  onSelectEntry: (
    entryPath: string,
    checked: boolean | "indeterminate",
  ) => void;
  onSortApply: (sortBy: ExplorerSortBy, sortOrder: ExplorerSortOrder) => void;
  onSortClear: () => void;
  onUpdateVisibility: (
    objectKey: string,
    visibility: ObjectVisibility,
  ) => Promise<void>;
  publishingPath: string;
  selectedPaths: Set<string>;
  selectionDisabled?: boolean;
  sortBy: ExplorerSortBy | null;
  sortOrder: ExplorerSortOrder | null;
}) {
  const { locale, t } = useI18n();
  const [scrollContainerElement, setScrollContainerElement] =
    useState<HTMLDivElement | null>(null);
  const [headerScrollbarOffset, setHeaderScrollbarOffset] = useState(0);
  const [fallbackScrollOffset, setFallbackScrollOffset] = useState(0);
  const [openSortBy, setOpenSortBy] = useState<ExplorerSortBy | null>(null);
  const [detailsPath, setDetailsPath] = useState<string | null>(null);
  const [deleteFilePath, setDeleteFilePath] = useState<string | null>(null);
  const [deleteFolderPath, setDeleteFolderPath] = useState<string | null>(null);
  const [publishFilePath, setPublishFilePath] = useState<string | null>(null);
  const [publishFolderPath, setPublishFolderPath] = useState<string | null>(
    null,
  );
  const selectedCount = entries.filter((entry) =>
    selectedPaths.has(entry.path),
  ).length;
  const allSelected = entries.length > 0 && selectedCount === entries.length;
  const partiallySelected = selectedCount > 0 && !allSelected;
  const detailsEntry = findFileEntry(entries, detailsPath);
  const deleteFileEntry = findFileEntry(entries, deleteFilePath);
  const deleteFolderEntry = findDirectoryEntry(entries, deleteFolderPath);
  const publishFileEntry = findFileEntry(entries, publishFilePath);
  const publishFolderEntry = findDirectoryEntry(entries, publishFolderPath);
  const rowVirtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => scrollContainerElement,
    estimateSize: () => explorerTableRowEstimate,
    getItemKey: (index) => entries[index]?.path ?? index,
    initialRect: {
      width: 0,
      height: 512,
    },
    overscan: explorerTableOverscan,
  });
  const virtualRows = rowVirtualizer.getVirtualItems();
  const fallbackRowCount = Math.min(
    entries.length,
    explorerTableOverscan * 2 + 1,
  );
  const fallbackStartIndex = Math.min(
    Math.floor(fallbackScrollOffset / explorerTableRowEstimate),
    Math.max(entries.length - fallbackRowCount, 0),
  );
  const fallbackEndIndex = Math.min(
    fallbackStartIndex + fallbackRowCount,
    entries.length,
  );
  const visibleRowIndexes =
    virtualRows.length > 0
      ? virtualRows.map((virtualRow) => virtualRow.index)
      : Array.from(
          { length: fallbackEndIndex - fallbackStartIndex },
          (_, index) => fallbackStartIndex + index,
        );
  const topSpacerHeight =
    virtualRows.length > 0 ? (virtualRows[0]?.start ?? 0) : 0;
  const bottomSpacerHeight =
    virtualRows.length > 0
      ? Math.max(
          rowVirtualizer.getTotalSize() -
            virtualRows[virtualRows.length - 1]!.end,
          0,
        )
      : Math.max(entries.length - fallbackEndIndex, 0) *
        explorerTableRowEstimate;
  const fallbackTopSpacerHeight = fallbackStartIndex * explorerTableRowEstimate;

  useEffect(() => {
    if (detailsPath && !detailsEntry) {
      setDetailsPath(null);
    }
    if (deleteFilePath && !deleteFileEntry) {
      setDeleteFilePath(null);
    }
    if (deleteFolderPath && !deleteFolderEntry) {
      setDeleteFolderPath(null);
    }
    if (publishFilePath && !publishFileEntry) {
      setPublishFilePath(null);
    }
    if (publishFolderPath && !publishFolderEntry) {
      setPublishFolderPath(null);
    }
  }, [
    deleteFileEntry,
    deleteFilePath,
    deleteFolderEntry,
    deleteFolderPath,
    detailsEntry,
    detailsPath,
    publishFileEntry,
    publishFilePath,
    publishFolderEntry,
    publishFolderPath,
  ]);

  useLayoutEffect(() => {
    if (!scrollContainerElement) {
      setHeaderScrollbarOffset(0);
      return;
    }

    const updateHeaderScrollbarOffset = () => {
      setHeaderScrollbarOffset(
        Math.max(
          scrollContainerElement.offsetWidth -
            scrollContainerElement.clientWidth,
          0,
        ),
      );
    };

    updateHeaderScrollbarOffset();
    window.addEventListener("resize", updateHeaderScrollbarOffset);

    return () => {
      window.removeEventListener("resize", updateHeaderScrollbarOffset);
    };
  }, [scrollContainerElement, entries.length]);

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <div className="min-h-0 flex-1 overflow-x-auto overflow-y-hidden">
        <div
          className={cn(
            "flex h-full min-h-0 flex-col",
            explorerTableMinWidthClass,
          )}
        >
          <div
            data-testid="explorer-table-header-container"
            style={{ paddingRight: headerScrollbarOffset }}
          >
            <table className="w-full table-fixed bg-card caption-bottom text-sm">
              <ExplorerTableColGroup />
              <TableHeader className="[&_tr]:border-b-0">
                <TableRow>
                  <TableHead
                    className={cn(
                      explorerHeaderCellClass,
                      explorerSelectionColumnWidthClass,
                    )}
                  >
                    <Checkbox
                      aria-label={t("explorer.selection.selectAll")}
                      disabled={selectionDisabled}
                      checked={
                        allSelected
                          ? true
                          : partiallySelected
                            ? "indeterminate"
                            : false
                      }
                      onCheckedChange={onSelectAll}
                    />
                  </TableHead>
                  <TableHead
                    className={cn(
                      explorerHeaderCellClass,
                      "w-[22.5rem] max-w-[22.5rem] text-base font-semibold text-muted-foreground",
                    )}
                  >
                    <ExplorerSortHeader
                      activeSortBy={sortBy}
                      label={t("explorer.table.name")}
                      onApply={onSortApply}
                      onClear={onSortClear}
                      open={openSortBy === "name"}
                      onOpenChange={(open) =>
                        setOpenSortBy(open ? "name" : null)
                      }
                      sortBy="name"
                      sortOrder={sortOrder}
                    />
                  </TableHead>
                  <TableHead
                    className={cn(
                      explorerHeaderCellClass,
                      "w-[17.5rem] text-base font-semibold text-muted-foreground",
                    )}
                  >
                    {t("explorer.table.url")}
                  </TableHead>
                  <TableHead
                    className={cn(
                      explorerHeaderCellClass,
                      "w-[7.5rem] text-base font-semibold text-muted-foreground",
                    )}
                  >
                    <ExplorerSortHeader
                      activeSortBy={sortBy}
                      label={t("explorer.table.size")}
                      onApply={onSortApply}
                      onClear={onSortClear}
                      open={openSortBy === "size"}
                      onOpenChange={(open) =>
                        setOpenSortBy(open ? "size" : null)
                      }
                      sortBy="size"
                      sortOrder={sortOrder}
                    />
                  </TableHead>
                  <TableHead
                    className={cn(
                      explorerHeaderCellClass,
                      "w-[10rem] text-base font-semibold text-muted-foreground",
                    )}
                  >
                    {t("objects.form.visibility.label")}
                  </TableHead>
                  <TableHead
                    className={cn(
                      explorerHeaderCellClass,
                      "w-[13.75rem] text-base font-semibold text-muted-foreground",
                    )}
                  >
                    <ExplorerSortHeader
                      activeSortBy={sortBy}
                      label={t("objects.table.createdAt")}
                      onApply={onSortApply}
                      onClear={onSortClear}
                      open={openSortBy === "created_at"}
                      onOpenChange={(open) =>
                        setOpenSortBy(open ? "created_at" : null)
                      }
                      sortBy="created_at"
                      sortOrder={sortOrder}
                    />
                  </TableHead>
                  <TableHead
                    className={cn(
                      explorerHeaderCellClass,
                      "w-[11.25rem] text-base font-semibold text-muted-foreground",
                    )}
                  >
                    {t("explorer.table.actions")}
                  </TableHead>
                </TableRow>
              </TableHeader>
            </table>
          </div>
          <div
            ref={setScrollContainerElement}
            className="min-h-0 flex-1 overflow-auto"
            data-testid="explorer-table-scroll-container"
            onScroll={(event) => {
              if (virtualRows.length === 0) {
                setFallbackScrollOffset(event.currentTarget.scrollTop);
              }
            }}
          >
            <table className="w-full table-fixed border-b border-border/70 bg-card caption-bottom text-sm">
              <ExplorerTableColGroup />
              <TableBody className="[&_tr:last-child]:border-b">
                {(virtualRows.length > 0
                  ? topSpacerHeight
                  : fallbackTopSpacerHeight) > 0 ? (
                  <ExplorerTableSpacerRow
                    height={
                      virtualRows.length > 0
                        ? topSpacerHeight
                        : fallbackTopSpacerHeight
                    }
                  />
                ) : null}
                {visibleRowIndexes.map((rowIndex) => {
                  const entry = entries[rowIndex];
                  if (!entry) {
                    return null;
                  }

                  return (
                    <ExplorerTableEntryRow
                      buildPublicUrl={buildPublicUrl}
                      deletingPath={deletingPath}
                      downloadingFilePath={downloadingFilePath}
                      downloadingFolderPath={downloadingFolderPath}
                      entry={entry}
                      key={entry.path}
                      locale={locale}
                      onDownloadFile={onDownloadFile}
                      onDownloadFolder={onDownloadFolder}
                      onOpenDeleteFile={setDeleteFilePath}
                      onOpenDeleteFolder={setDeleteFolderPath}
                      onOpenDetails={setDetailsPath}
                      onOpenDirectory={onOpenDirectory}
                      onOpenPublishFile={setPublishFilePath}
                      onOpenPublishFolder={setPublishFolderPath}
                      onSelectEntry={onSelectEntry}
                      publishingPath={publishingPath}
                      selected={selectedPaths.has(entry.path)}
                      selectionDisabled={selectionDisabled}
                    />
                  );
                })}
                {bottomSpacerHeight > 0 ? (
                  <ExplorerTableSpacerRow height={bottomSpacerHeight} />
                ) : null}
              </TableBody>
            </table>
          </div>
        </div>
      </div>
      <FileDetailsDialog
        buildPublicUrl={buildPublicUrl}
        entry={detailsEntry}
        onOpenChange={(open) => {
          if (!open) {
            setDetailsPath(null);
          }
        }}
        onUpdateVisibility={onUpdateVisibility}
        open={detailsEntry !== null}
      />
      <DeleteFileDialog
        bucket={bucket}
        entry={deleteFileEntry}
        onDeleteFile={async (objectKey) => {
          setDeleteFilePath(null);
          await onDeleteFile(objectKey);
        }}
        onOpenChange={(open) => {
          if (!open) {
            setDeleteFilePath(null);
          }
        }}
      />
      <DeleteFolderDialog
        bucket={bucket}
        entry={deleteFolderEntry}
        onDeleteFolder={async (folderPath) => {
          setDeleteFolderPath(null);
          await onDeleteFolder(folderPath);
        }}
        onOpenChange={(open) => {
          if (!open) {
            setDeleteFolderPath(null);
          }
        }}
      />
      {publishFolderEntry ? (
        <PublishSiteDialog
          bucket={bucket}
          onOpenChange={(open) => {
            if (!open) {
              setPublishFolderPath(null);
            }
          }}
          onSubmit={(value) => onPublishSite(publishFolderEntry.path, value)}
          open
          pending={publishingPath === publishFolderEntry.path}
          prefix={publishFolderEntry.path}
          trigger={null}
        />
      ) : null}
      {publishFileEntry ? (
        <PublishObjectSiteDialog
          bucket={bucket}
          objectKey={publishFileEntry.object_key}
          onOpenChange={(open) => {
            if (!open) {
              setPublishFilePath(null);
            }
          }}
          onSubmit={(value) =>
            onPublishObjectSite(publishFileEntry.object_key, value)
          }
          open
          pending={publishingPath === publishFileEntry.path}
          trigger={null}
        />
      ) : null}
    </div>
  );
}

function ExplorerTableColGroup() {
  return (
    <colgroup>
      <col className={explorerSelectionColumnWidthClass} />
      <col className="w-[22.5rem]" />
      <col className="w-[17.5rem]" />
      <col className="w-[7.5rem]" />
      <col className="w-[10rem]" />
      <col className="w-[13.75rem]" />
      <col className="w-[11.25rem]" />
    </colgroup>
  );
}

function ExplorerTableSpacerRow({ height }: { height: number }) {
  return (
    <TableRow aria-hidden="true" className="hover:bg-transparent">
      <TableCell
        className="p-0"
        colSpan={explorerTableColumnCount}
        style={{ height }}
      />
    </TableRow>
  );
}

function ExplorerTableEntryRow({
  buildPublicUrl,
  deletingPath,
  downloadingFilePath,
  downloadingFolderPath,
  entry,
  locale,
  onDownloadFile,
  onDownloadFolder,
  onOpenDeleteFile,
  onOpenDeleteFolder,
  onOpenDetails,
  onOpenDirectory,
  onOpenPublishFile,
  onOpenPublishFolder,
  onSelectEntry,
  publishingPath,
  selected,
  selectionDisabled,
}: {
  buildPublicUrl: (objectKey: string) => string;
  deletingPath: string;
  downloadingFilePath: string;
  downloadingFolderPath: string;
  entry: ExplorerEntry;
  locale: AppLocale;
  onDownloadFile: (entry: ExplorerFileEntry) => Promise<void>;
  onDownloadFolder: (folderPath: string) => Promise<void>;
  onOpenDeleteFile: (entryPath: string) => void;
  onOpenDeleteFolder: (entryPath: string) => void;
  onOpenDetails: (entryPath: string) => void;
  onOpenDirectory: (folderPath: string) => void;
  onOpenPublishFile: (entryPath: string) => void;
  onOpenPublishFolder: (entryPath: string) => void;
  onSelectEntry: (
    entryPath: string,
    checked: boolean | "indeterminate",
  ) => void;
  publishingPath: string;
  selected: boolean;
  selectionDisabled: boolean;
}) {
  const { t } = useI18n();

  return (
    <TableRow data-state={selected ? "selected" : undefined}>
      <TableCell className={explorerSelectionColumnWidthClass}>
        <Checkbox
          aria-label={t("explorer.selection.selectRow", {
            name: entry.name,
          })}
          checked={selected}
          disabled={selectionDisabled}
          onCheckedChange={(checked) => onSelectEntry(entry.path, checked)}
        />
      </TableCell>
      <TableCell className="w-[22.5rem] max-w-[22.5rem]">
        <ExplorerEntryName
          entry={entry}
          onOpenDetails={onOpenDetails}
          onOpenDirectory={onOpenDirectory}
        />
      </TableCell>
      <TableCell className="max-w-[17.5rem]">
        <ExplorerEntryUrlCell
          buildPublicUrl={buildPublicUrl}
          copyLabel={t("explorer.actions.copyUrl")}
          entry={entry}
        />
      </TableCell>
      <TableCell
        className={
          entry.type === "directory" ? "text-muted-foreground" : undefined
        }
      >
        {entry.type === "directory" ? "-" : formatBytes(entry.size)}
      </TableCell>
      <TableCell
        className={
          entry.type === "directory" ? "text-muted-foreground" : undefined
        }
      >
        {entry.type === "directory" ? (
          "-"
        ) : (
          <Badge
            className="flex items-center"
            variant={entry.visibility === "public" ? "outline" : "secondary"}
          >
            {entry.visibility === "public" ? (
              <>
                <UnlockIcon />
                {t("objects.visibility.public")}
              </>
            ) : (
              <>
                <LockIcon />
                {t("objects.visibility.private")}
              </>
            )}
          </Badge>
        )}
      </TableCell>
      <TableCell
        className={
          entry.type === "directory" ? "text-muted-foreground" : undefined
        }
      >
        {entry.type === "directory"
          ? "-"
          : formatDate(entry.created_at, locale)}
      </TableCell>
      <TableCell>
        {entry.type === "directory" ? (
          <ExplorerDirectoryActions
            deletingPath={deletingPath}
            downloadingFolderPath={downloadingFolderPath}
            entry={entry}
            onDownloadFolder={onDownloadFolder}
            onOpenDeleteFolder={onOpenDeleteFolder}
            onOpenDirectory={onOpenDirectory}
            onOpenPublishSite={onOpenPublishFolder}
            publishingPath={publishingPath}
          />
        ) : (
          <ExplorerFileActions
            deletingPath={deletingPath}
            downloadingFilePath={downloadingFilePath}
            entry={entry}
            onDownloadFile={onDownloadFile}
            onOpenDeleteFile={onOpenDeleteFile}
            onOpenDetails={onOpenDetails}
            onOpenPublishObjectSite={onOpenPublishFile}
            publishingPath={publishingPath}
          />
        )}
      </TableCell>
    </TableRow>
  );
}

function ExplorerEntryName({
  entry,
  onOpenDetails,
  onOpenDirectory,
}: {
  entry: ExplorerEntry;
  onOpenDetails: (entryPath: string) => void;
  onOpenDirectory: (folderPath: string) => void;
}) {
  if (entry.type === "directory") {
    return (
      <Button
        className="px-0 gap-3 flex max-w-full w-fit min-w-0 justify-start items-center font-normal select-text hover:bg-transparent"
        onClick={() => onOpenDirectory(entry.path)}
        type="button"
        variant="ghost"
      >
        <span className="inline-flex size-4 items-center justify-center text-amber-500 [&_svg]:size-4">
          <FileDirectoryFillIcon />
        </span>
        <span className="min-w-0 truncate">{entry.name}</span>
      </Button>
    );
  }

  if (entry.type === "file") {
    return (
      <Button
        className="px-0 gap-3 flex max-w-full w-fit min-w-0 justify-start items-center font-normal select-text hover:bg-transparent"
        onClick={() => onOpenDetails(entry.path)}
        type="button"
        variant="ghost"
      >
        <span className="inline-flex size-4 items-center justify-center text-gray-500 [&_svg]:size-4">
          <FileIcon />
        </span>
        <span className="min-w-0 truncate">{entry.name}</span>
      </Button>
    );
  }

  const exhaustiveEntry: never = entry;
  return exhaustiveEntry;
}

function ExplorerDirectoryActions({
  deletingPath,
  downloadingFolderPath,
  entry,
  onDownloadFolder,
  onOpenDeleteFolder,
  onOpenDirectory,
  onOpenPublishSite,
  publishingPath,
}: {
  deletingPath: string;
  downloadingFolderPath: string;
  entry: ExplorerDirectoryEntry;
  onDownloadFolder: (folderPath: string) => Promise<void>;
  onOpenDeleteFolder: (entryPath: string) => void;
  onOpenDirectory: (folderPath: string) => void;
  onOpenPublishSite: (entryPath: string) => void;
  publishingPath: string;
}) {
  const { t } = useI18n();
  const deleting = deletingPath === entry.path;

  return (
    <div className="flex items-center justify-start gap-1">
      <ExplorerIconButton
        label={t("explorer.actions.openFolder")}
        onClick={() => onOpenDirectory(entry.path)}
      >
        <FileDirectoryOpenFillIcon className="text-amber-500" />
      </ExplorerIconButton>

      <DownloadFolderZipButton
        downloadingFolderPath={downloadingFolderPath}
        entry={entry}
        onDownloadFolder={onDownloadFolder}
      />

      <PublishFolderSiteButton
        entry={entry}
        onOpenPublishSite={onOpenPublishSite}
        publishingPath={publishingPath}
      />

      <ExplorerOverflowMenu label={t("explorer.actions.more")}>
        <DropdownMenuItem
          className="cursor-pointer"
          disabled={deleting}
          onSelect={() => onOpenDeleteFolder(entry.path)}
          variant="destructive"
        >
          {deleting ? <SyncIcon className="animate-spin" /> : <TrashIcon />}
          {t("explorer.actions.deleteFolder")}
        </DropdownMenuItem>
      </ExplorerOverflowMenu>
    </div>
  );
}

function ExplorerFileActions({
  deletingPath,
  downloadingFilePath,
  entry,
  onDownloadFile,
  onOpenDeleteFile,
  onOpenDetails,
  onOpenPublishObjectSite,
  publishingPath,
}: {
  deletingPath: string;
  downloadingFilePath: string;
  entry: ExplorerFileEntry;
  onDownloadFile: (entry: ExplorerFileEntry) => Promise<void>;
  onOpenDeleteFile: (entryPath: string) => void;
  onOpenDetails: (entryPath: string) => void;
  onOpenPublishObjectSite: (entryPath: string) => void;
  publishingPath: string;
}) {
  const { t } = useI18n();
  const deleting = deletingPath === entry.object_key;
  const downloading = downloadingFilePath === entry.path;
  const downloadLabel = downloading
    ? t("explorer.actions.downloadingFile")
    : entry.visibility === "public"
      ? t("explorer.actions.directDownload")
      : t("explorer.actions.signedDownload");

  return (
    <div className="flex items-center justify-start gap-1">
      <ExplorerIconButton
        label={t("explorer.actions.viewDetails")}
        onClick={() => onOpenDetails(entry.path)}
      >
        <EyeIcon className="text-muted-foreground" />
      </ExplorerIconButton>

      <ExplorerIconButton
        disabled={downloading}
        label={downloadLabel}
        onClick={() => void onDownloadFile(entry)}
      >
        {downloading ? (
          <SyncIcon className="animate-spin text-sky-500" />
        ) : (
          <DownloadIcon className="text-sky-500" />
        )}
      </ExplorerIconButton>

      <PublishObjectSiteButton
        entry={entry}
        onOpenPublishObjectSite={onOpenPublishObjectSite}
        publishingPath={publishingPath}
      />

      <ExplorerOverflowMenu label={t("explorer.actions.more")}>
        <DropdownMenuItem
          className="cursor-pointer"
          disabled={deleting}
          onSelect={() => onOpenDeleteFile(entry.path)}
          variant="destructive"
        >
          {deleting ? <SyncIcon className="animate-spin" /> : <TrashIcon />}
          {t("common.delete")}
        </DropdownMenuItem>
      </ExplorerOverflowMenu>
    </div>
  );
}

function ExplorerOverflowMenu({
  children,
  label,
}: {
  children: React.ReactNode;
  label: string;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          aria-label={label}
          className="[&_svg]:size-4"
          size="icon-sm"
          type="button"
          variant="ghost"
        >
          <KebabHorizontalIcon />
          <span className="sr-only">{label}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuGroup>{children}</DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ExplorerSortHeader({
  activeSortBy,
  label,
  onApply,
  onClear,
  onOpenChange,
  open,
  sortBy,
  sortOrder,
}: {
  activeSortBy: ExplorerSortBy | null;
  label: string;
  onApply: (sortBy: ExplorerSortBy, sortOrder: ExplorerSortOrder) => void;
  onClear: () => void;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  sortBy: ExplorerSortBy;
  sortOrder: ExplorerSortOrder | null;
}) {
  const { t } = useI18n();
  const active = activeSortBy === sortBy;
  const appliedSortOrder = active ? (sortOrder ?? "asc") : "asc";
  const [draftSortOrder, setDraftSortOrder] =
    useState<ExplorerSortOrder | null>(active ? appliedSortOrder : null);
  const triggerState = active
    ? appliedSortOrder === "asc"
      ? t("explorer.sort.state.asc")
      : t("explorer.sort.state.desc")
    : t("explorer.sort.state.none");
  const popoverTitle = t("explorer.sort.popover.title", { label });
  const hasActiveSort = activeSortBy !== null;
  const clearDisabled = !hasActiveSort;
  const Icon = !active
    ? FilterIcon
    : appliedSortOrder === "asc"
      ? SortAscIcon
      : SortDescIcon;

  useEffect(() => {
    if (!open) {
      return;
    }
    setDraftSortOrder(active ? appliedSortOrder : null);
  }, [active, appliedSortOrder, open]);

  function handleConfirm() {
    if (!draftSortOrder) {
      toast.error(t("explorer.sort.validation.required"));
      return;
    }

    onApply(sortBy, draftSortOrder);
    onOpenChange(false);
  }

  function handleCancel() {
    onOpenChange(false);
  }

  function handleClear() {
    if (clearDisabled) {
      onOpenChange(false);
      return;
    }

    onClear();
    onOpenChange(false);
  }

  return (
    <div className="flex items-center gap-1">
      <span>{label}</span>
      <Popover open={open} onOpenChange={onOpenChange}>
        <PopoverTrigger asChild>
          <Button
            aria-label={t("explorer.sort.trigger", {
              label,
              state: triggerState,
            })}
            aria-pressed={active}
            className={cn(
              "size-6 text-muted-foreground hover:text-foreground",
              active && "text-foreground",
            )}
            size="icon-xs"
            type="button"
            variant="ghost"
          >
            <Icon className="size-3.5" />
          </Button>
        </PopoverTrigger>
        <PopoverContent
          align="start"
          aria-label={popoverTitle}
          className="w-64 gap-4"
          sideOffset={8}
        >
          <PopoverHeader>
            <PopoverTitle>{popoverTitle}</PopoverTitle>
            <PopoverDescription>
              {t("explorer.sort.popover.description")}
            </PopoverDescription>
          </PopoverHeader>
          <div className="flex flex-col gap-3">
            <ToggleGroup
              aria-label={popoverTitle}
              className="w-full"
              onValueChange={(value) => {
                if (value === "asc" || value === "desc") {
                  setDraftSortOrder(value);
                }
              }}
              type="single"
              value={draftSortOrder ?? ""}
              variant="outline"
            >
              <ToggleGroupItem className="flex-1 cursor-pointer" value="asc">
                {t("explorer.sort.option.asc")}
              </ToggleGroupItem>
              <ToggleGroupItem className="flex-1 cursor-pointer" value="desc">
                {t("explorer.sort.option.desc")}
              </ToggleGroupItem>
            </ToggleGroup>
            <div className="flex items-center justify-between gap-2">
              <Button
                disabled={clearDisabled}
                onClick={handleClear}
                type="button"
                variant="ghost"
              >
                {t("explorer.sort.clear")}
              </Button>
              <div className="flex items-center gap-2">
                <Button onClick={handleCancel} type="button" variant="outline">
                  {t("explorer.sort.cancel")}
                </Button>
                <Button onClick={handleConfirm} type="button">
                  {t("explorer.sort.confirm")}
                </Button>
              </div>
            </div>
          </div>
        </PopoverContent>
      </Popover>
    </div>
  );
}

function ExplorerEntryUrlCell({
  buildPublicUrl,
  copyLabel,
  entry,
}: {
  buildPublicUrl: (objectKey: string) => string;
  copyLabel: string;
  entry: ExplorerEntry;
}) {
  const { t } = useI18n();

  if (entry.type !== "file" || entry.visibility !== "public") {
    return <span className="text-muted-foreground">-</span>;
  }

  const url = buildPublicUrl(entry.object_key);

  async function handleCopyUrl() {
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error("clipboard_unavailable");
      }

      await navigator.clipboard.writeText(url);
      toast.success(t("toast.urlCopied"));
    } catch {
      toast.error(t("errors.copyUrl"));
    }
  }

  return (
    <div className="flex min-w-0 items-center gap-1">
      <a
        className="block min-w-0 flex-1 truncate text-sky-600 hover:underline"
        href={url}
        rel="noreferrer"
        target="_blank"
        title={url}
      >
        {url}
      </a>
      <ExplorerIconButton
        label={copyLabel}
        onClick={() => {
          void handleCopyUrl();
        }}
      >
        <CopyIcon className="text-sky-500" />
      </ExplorerIconButton>
    </div>
  );
}

function FileDetailsDialog({
  buildPublicUrl,
  entry,
  onOpenChange,
  onUpdateVisibility,
  open,
}: {
  buildPublicUrl: (objectKey: string) => string;
  entry: ExplorerFileEntry | null;
  onOpenChange: (open: boolean) => void;
  onUpdateVisibility: (
    objectKey: string,
    visibility: ObjectVisibility,
  ) => Promise<void>;
  open: boolean;
}) {
  const [selectedVisibility, setSelectedVisibility] =
    useState<ObjectVisibility>("private");
  const [currentVisibility, setCurrentVisibility] =
    useState<ObjectVisibility>("private");
  const [isSavingVisibility, setIsSavingVisibility] = useState(false);
  const { locale, t } = useI18n();
  const publicUrl =
    entry && currentVisibility === "public"
      ? buildPublicUrl(entry.object_key)
      : "";
  const previewType = entry ? getPreviewType(entry) : null;
  const markdownPreviewTooLarge =
    entry !== null &&
    previewType === "markdown" &&
    entry.size > MAX_MARKDOWN_PREVIEW_BYTES;
  const textPreviewTooLarge =
    entry !== null &&
    previewType === "text" &&
    entry.size > MAX_TEXT_PREVIEW_BYTES;

  useEffect(() => {
    if (!entry) {
      return;
    }

    setSelectedVisibility(entry.visibility);
    setCurrentVisibility(entry.visibility);
  }, [entry]);

  if (!entry) {
    return null;
  }

  async function handleSaveVisibility() {
    if (
      !entry ||
      selectedVisibility === currentVisibility ||
      isSavingVisibility
    ) {
      return;
    }

    setIsSavingVisibility(true);
    try {
      await onUpdateVisibility(entry.object_key, selectedVisibility);
      setCurrentVisibility(selectedVisibility);
      toast.success(t("toast.objectVisibilityUpdated"));
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : t("errors.updateObjectVisibility");
      toast.error(message);
    } finally {
      setIsSavingVisibility(false);
    }
  }

  return (
    <Dialog onOpenChange={onOpenChange} open={open}>
      <DialogContent
        aria-describedby={undefined}
        className="max-h-[85vh] min-w-0 sm:max-w-xl"
      >
        <DialogHeader>
          <DialogTitle>{t("explorer.details.title")}</DialogTitle>
        </DialogHeader>

        <div className="-mr-1 min-w-0 overflow-y-auto pr-1">
          <dl className="grid gap-3">
            <DetailField label={t("explorer.details.preview")}>
              <div className="flex min-w-0 max-w-full flex-col">
                <FilePreview
                  fileName={entry.original_filename}
                  displayMode="inline"
                  markdownPreviewTooLarge={markdownPreviewTooLarge}
                  previewType={previewType}
                  publicUrl={publicUrl}
                  textPreviewTooLarge={textPreviewTooLarge}
                />
              </div>
            </DetailField>
            <DetailField label={t("explorer.table.url")} monospace>
              {publicUrl ? (
                <a
                  className="min-w-0 wrap-anywhere text-sky-600 hover:underline"
                  href={publicUrl}
                  rel="noreferrer"
                  target="_blank"
                >
                  {publicUrl}
                </a>
              ) : (
                t("common.notAvailable")
              )}
            </DetailField>
            <DetailField
              label={t("explorer.details.originalFilename")}
              monospace
            >
              {entry.original_filename}
            </DetailField>
            <DetailField label={t("explorer.details.contentType")} monospace>
              {entry.content_type}
            </DetailField>
            <DetailField label={t("objects.table.size")}>
              {formatBytes(entry.size)}
            </DetailField>
            <DetailField label={t("objects.table.visibility")}>
              <div className="flex flex-col gap-2">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                  <Select
                    onValueChange={(value) =>
                      setSelectedVisibility(value as ObjectVisibility)
                    }
                    value={selectedVisibility}
                  >
                    <SelectTrigger
                      aria-label={t("objects.form.visibility.label")}
                      className="w-full sm:w-[160px]"
                      disabled={isSavingVisibility}
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent position="popper" side="bottom">
                      <SelectGroup>
                        <SelectItem value="private">
                          {t("objects.visibility.private")}
                        </SelectItem>
                        <SelectItem value="public">
                          {t("objects.visibility.public")}
                        </SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <Button
                    disabled={
                      isSavingVisibility ||
                      selectedVisibility === currentVisibility
                    }
                    onClick={() => {
                      void handleSaveVisibility();
                    }}
                    type="button"
                    variant="outline"
                  >
                    {isSavingVisibility ? (
                      <SyncIcon
                        className="animate-spin"
                        data-icon="inline-start"
                      />
                    ) : null}
                    {t("common.save")}
                  </Button>
                </div>
              </div>
            </DetailField>
            <div className="flex flex-col gap-3 sm:flex-row">
              <DetailField
                className="flex-1"
                label={t("explorer.table.updatedAt")}
              >
                {formatDate(entry.updated_at, locale)}
              </DetailField>
              <DetailField
                className="flex-1"
                label={t("objects.table.createdAt")}
              >
                {formatDate(entry.created_at ?? entry.updated_at, locale)}
              </DetailField>
            </div>
          </dl>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function DetailField({
  children,
  className,
  label,
  monospace,
}: {
  children: React.ReactNode;
  className?: string;
  label: string;
  monospace?: boolean;
}) {
  return (
    <div
      className={cn(
        "min-w-0 rounded-lg border border-border/70 bg-muted/30 p-3",
        className,
      )}
    >
      <dt className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </dt>
      <dd
        className={cn(
          "mt-1 min-w-0 text-sm wrap-anywhere",
          monospace && "font-mono",
        )}
      >
        {children}
      </dd>
    </div>
  );
}

type PreviewType =
  | "image"
  | "video"
  | "audio"
  | "pdf"
  | "markdown"
  | "text"
  | null;
type PreviewDisplayMode = "inline" | "fullscreen";
const MAX_MARKDOWN_PREVIEW_BYTES = 100 * 1024;
const MAX_TEXT_PREVIEW_BYTES = 100 * 1024;

const openXmlOfficeExtensions = new Set([
  ".docx",
  ".dotx",
  ".docm",
  ".dotm",
  ".xlsx",
  ".xltx",
  ".xlsm",
  ".xltm",
  ".pptx",
  ".potx",
  ".ppsx",
  ".pptm",
  ".potm",
  ".ppsm",
  ".ppam",
]);

const markdownComponents: Components = {
  h1: ({ children }) => (
    <h1 className="mt-0 text-xl font-semibold text-foreground">{children}</h1>
  ),
  h2: ({ children }) => (
    <h2 className="mt-6 text-lg font-semibold text-foreground">{children}</h2>
  ),
  h3: ({ children }) => (
    <h3 className="mt-5 text-base font-semibold text-foreground">{children}</h3>
  ),
  p: ({ children }) => <p className="leading-7 text-foreground">{children}</p>,
  ul: ({ children }) => (
    <ul className="flex list-disc flex-col gap-2 pl-5">{children}</ul>
  ),
  ol: ({ children }) => (
    <ol className="flex list-decimal flex-col gap-2 pl-5">{children}</ol>
  ),
  li: ({ children }) => <li className="leading-7">{children}</li>,
  blockquote: ({ children }) => (
    <blockquote className="border-l-2 border-border/70 pl-4 italic text-muted-foreground">
      {children}
    </blockquote>
  ),
  hr: () => <hr className="border-border/70" />,
  a: ({ children, ...props }) => (
    <a
      {...props}
      className="font-medium text-sky-600 underline underline-offset-4 hover:text-sky-500"
      rel="noreferrer"
      target="_blank"
    >
      {children}
    </a>
  ),
  table: ({ children }) => (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-sm">{children}</table>
    </div>
  ),
  th: ({ children }) => (
    <th className="border border-border/70 bg-muted/40 px-3 py-2 text-left font-medium">
      {children}
    </th>
  ),
  td: ({ children }) => (
    <td className="border border-border/70 px-3 py-2 align-top">{children}</td>
  ),
  pre: ({ children }) => (
    <pre className="overflow-x-auto rounded-md border border-border/70 bg-muted/60 p-3">
      {children}
    </pre>
  ),
  code: ({ children, className }) => {
    const content = String(children).replace(/\n$/, "");
    const isInline = !className && !content.includes("\n");

    if (isInline) {
      return (
        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.85em]">
          {content}
        </code>
      );
    }

    return (
      <code className={`font-mono text-sm ${className ?? ""}`.trim()}>
        {content}
      </code>
    );
  },
};

function PreviewFullscreenButton({
  fileName,
  markdownPreviewTooLarge = false,
  previewType,
  publicUrl,
  textPreviewTooLarge = false,
}: {
  fileName: string;
  markdownPreviewTooLarge?: boolean;
  previewType: PreviewType;
  publicUrl: string;
  textPreviewTooLarge?: boolean;
}) {
  const { t } = useI18n();
  const label = t("explorer.actions.fullscreenPreview");

  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button
          className="rounded-full bg-background/75 text-foreground shadow-sm backdrop-blur-sm hover:bg-background/90 hover:shadow-md focus-visible:bg-background/90"
          size="icon-sm"
          type="button"
          variant="ghost"
        >
          <ScreenFullIcon />
          <span className="sr-only">{label}</span>
        </Button>
      </DialogTrigger>
      <DialogContent
        aria-describedby={undefined}
        className="flex h-[96vh] w-[96vw] max-w-[1300px] flex-col gap-3 p-5"
      >
        <DialogHeader className="pr-10">
          <DialogTitle>{fileName}</DialogTitle>
        </DialogHeader>
        <div className="min-h-0 min-w-0 flex-1">
          <FilePreview
            fileName={fileName}
            displayMode="fullscreen"
            markdownPreviewTooLarge={markdownPreviewTooLarge}
            previewType={previewType}
            publicUrl={publicUrl}
            textPreviewTooLarge={textPreviewTooLarge}
          />
        </div>
      </DialogContent>
    </Dialog>
  );
}

function FilePreview({
  fileName,
  displayMode = "inline",
  markdownPreviewTooLarge = false,
  previewType,
  publicUrl,
  textPreviewTooLarge = false,
}: {
  fileName: string;
  displayMode?: PreviewDisplayMode;
  markdownPreviewTooLarge?: boolean;
  previewType: PreviewType;
  publicUrl: string;
  textPreviewTooLarge?: boolean;
}) {
  const { t } = useI18n();
  const isFullscreen = displayMode === "fullscreen";
  const inlineAction =
    !isFullscreen &&
    previewType !== "audio" &&
    !(previewType === "markdown" && markdownPreviewTooLarge) &&
    !(previewType === "text" && textPreviewTooLarge) ? (
      <PreviewFullscreenButton
        fileName={fileName}
        markdownPreviewTooLarge={markdownPreviewTooLarge}
        previewType={previewType}
        publicUrl={publicUrl}
        textPreviewTooLarge={textPreviewTooLarge}
      />
    ) : null;

  if (!publicUrl || !previewType) {
    return (
      <span className="text-muted-foreground">{t("common.notAvailable")}</span>
    );
  }

  const renderInlineSurface = (children: React.ReactNode) => (
    <div
      className="relative min-w-0 max-w-full overflow-hidden rounded-md border border-border/70 bg-background"
      data-testid="inline-preview-surface"
    >
      {inlineAction ? (
        <div className="absolute top-3 right-5 z-10">{inlineAction}</div>
      ) : null}
      {children}
    </div>
  );

  if (previewType === "image") {
    const imagePreview = (
      <img
        alt="file preview"
        className={cn(
          "w-full min-w-0 max-w-full object-contain",
          isFullscreen
            ? "h-full max-h-full rounded-md border border-border/70 bg-background"
            : "max-h-80",
        )}
        src={publicUrl}
      />
    );

    return isFullscreen ? imagePreview : renderInlineSurface(imagePreview);
  }

  if (previewType === "video") {
    const videoPreview = (
      <video
        className={cn(
          "w-full min-w-0 max-w-full",
          isFullscreen
            ? "h-full max-h-full rounded-md border border-border/70 bg-background"
            : "max-h-80",
        )}
        controls
        src={publicUrl}
      />
    );

    return isFullscreen ? videoPreview : renderInlineSurface(videoPreview);
  }

  if (previewType === "audio") {
    return (
      <audio className="w-full min-w-0 max-w-full" controls src={publicUrl} />
    );
  }

  if (previewType === "markdown") {
    return (
      <RemoteTextPreview
        action={inlineAction}
        displayMode={displayMode}
        markdownPreviewTooLarge={markdownPreviewTooLarge}
        mode="markdown"
        publicUrl={publicUrl}
        textPreviewTooLarge={textPreviewTooLarge}
      />
    );
  }

  if (previewType === "text") {
    return (
      <RemoteTextPreview
        action={inlineAction}
        displayMode={displayMode}
        mode="text"
        publicUrl={publicUrl}
        textPreviewTooLarge={textPreviewTooLarge}
      />
    );
  }

  const previewUrl =
    previewType === "pdf"
      ? buildPdfPreviewUrl(publicUrl, displayMode)
      : publicUrl;

  const iframePreview = (
    <iframe
      className={cn(
        "w-full min-w-0 max-w-full bg-background",
        isFullscreen
          ? "h-full min-h-0 rounded-md border border-border/70"
          : "h-80",
      )}
      src={previewUrl}
      title="file preview"
    />
  );

  return isFullscreen ? iframePreview : renderInlineSurface(iframePreview);
}

function buildPdfPreviewUrl(
  publicUrl: string,
  displayMode: PreviewDisplayMode,
) {
  const params =
    displayMode === "fullscreen"
      ? "toolbar=0&navpanes=0&pagemode=none&zoom=page-width"
      : "toolbar=0&navpanes=0&pagemode=none&view=Fit&zoom=page-fit";
  const separator = publicUrl.includes("#") ? "&" : "#";

  return `${publicUrl}${separator}${params}`;
}

function RemoteTextPreview({
  action,
  displayMode,
  markdownPreviewTooLarge = false,
  mode,
  publicUrl,
  textPreviewTooLarge = false,
}: {
  action?: React.ReactNode;
  displayMode: PreviewDisplayMode;
  markdownPreviewTooLarge?: boolean;
  mode: "markdown" | "text";
  publicUrl: string;
  textPreviewTooLarge?: boolean;
}) {
  const { t } = useI18n();
  const [previewText, setPreviewText] = useState("");
  const [status, setStatus] = useState<"loading" | "ready" | "error">(
    "loading",
  );

  useEffect(() => {
    if (
      (mode === "markdown" && markdownPreviewTooLarge) ||
      (mode === "text" && textPreviewTooLarge)
    ) {
      setPreviewText("");
      setStatus("ready");
      return;
    }

    const controller = new AbortController();

    setPreviewText("");
    setStatus("loading");

    void fetch(publicUrl, {
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error(`preview_request_failed_${response.status}`);
        }

        return response.text();
      })
      .then((text) => {
        setPreviewText(text);
        setStatus("ready");
      })
      .catch((error: unknown) => {
        if ((error as { name?: string } | null)?.name === "AbortError") {
          return;
        }

        setStatus("error");
      });

    return () => {
      controller.abort();
    };
  }, [markdownPreviewTooLarge, mode, publicUrl, textPreviewTooLarge]);

  if (
    (mode === "markdown" && markdownPreviewTooLarge) ||
    (mode === "text" && textPreviewTooLarge)
  ) {
    const tooLargeMessage =
      mode === "markdown"
        ? t("explorer.preview.markdownTooLarge")
        : t("explorer.preview.textTooLarge");

    if (displayMode === "inline") {
      return (
        <div
          className="relative min-w-0 max-w-full rounded-md border border-border/70 bg-background p-4"
          data-testid="inline-preview-surface"
        >
          {action ? (
            <div className="absolute top-3 right-5 z-10">{action}</div>
          ) : null}
          <span className="text-muted-foreground">{tooLargeMessage}</span>
        </div>
      );
    }

    return <span className="text-muted-foreground">{tooLargeMessage}</span>;
  }

  if (status === "loading") {
    if (displayMode === "inline") {
      return (
        <div
          className="relative min-w-0 max-w-full rounded-md border border-border/70 bg-background p-4 pt-12"
          data-testid="inline-preview-surface"
        >
          {action ? (
            <div className="absolute top-3 right-5 z-10">{action}</div>
          ) : null}
          <span className="text-muted-foreground">{t("common.loading")}</span>
        </div>
      );
    }

    return <span className="text-muted-foreground">{t("common.loading")}</span>;
  }

  if (status === "error") {
    if (displayMode === "inline") {
      return (
        <div
          className="relative min-w-0 max-w-full rounded-md border border-border/70 bg-background p-4 pt-12"
          data-testid="inline-preview-surface"
        >
          {action ? (
            <div className="absolute top-3 right-5 z-10">{action}</div>
          ) : null}
          <span className="text-muted-foreground">
            {t("common.notAvailable")}
          </span>
        </div>
      );
    }

    return (
      <span className="text-muted-foreground">{t("common.notAvailable")}</span>
    );
  }

  if (mode === "markdown") {
    if (displayMode === "inline") {
      return (
        <div
          className="relative min-w-0 max-w-full rounded-md border border-border/70 bg-background"
          data-testid="inline-preview-surface"
        >
          {action ? (
            <div className="absolute top-3 right-5 z-10">{action}</div>
          ) : null}
          <div className="max-h-80 min-w-0 max-w-full overflow-auto p-4 pt-12">
            <div className="flex min-w-0 flex-col gap-4 text-sm">
              <ReactMarkdown
                components={markdownComponents}
                remarkPlugins={[remarkGfm]}
                skipHtml
              >
                {previewText}
              </ReactMarkdown>
            </div>
          </div>
        </div>
      );
    }

    return (
      <div className="h-full min-h-0 min-w-0 max-w-full overflow-auto rounded-md border border-border/70 bg-background p-4">
        <div className="flex min-w-0 flex-col gap-4 text-sm">
          <ReactMarkdown
            components={markdownComponents}
            remarkPlugins={[remarkGfm]}
            skipHtml
          >
            {previewText}
          </ReactMarkdown>
        </div>
      </div>
    );
  }

  if (displayMode === "inline") {
    return (
      <div
        className="relative min-w-0 max-w-full rounded-md border border-border/70 bg-background"
        data-testid="inline-preview-surface"
      >
        {action ? (
          <div className="absolute top-3 right-5 z-10">{action}</div>
        ) : null}
        <div className="max-h-80 min-w-0 max-w-full overflow-auto">
          <pre className="w-full min-w-0 max-w-full p-3 pt-12 font-mono text-sm whitespace-pre-wrap wrap-break-word">
            {previewText}
          </pre>
        </div>
      </div>
    );
  }

  return (
    <pre className="h-full min-h-0 w-full min-w-0 max-w-full overflow-auto rounded-md border border-border/70 bg-background p-3 font-mono text-sm whitespace-pre-wrap wrap-break-word">
      {previewText}
    </pre>
  );
}

function getPreviewType(entry: ExplorerFileEntry): PreviewType {
  const contentType = entry.content_type.toLowerCase();
  const mimeType = contentType.split(";")[0]?.trim() ?? "";
  const name = entry.original_filename.toLowerCase();

  if (mimeType.startsWith("image/")) {
    return "image";
  }
  if (mimeType.startsWith("video/")) {
    return "video";
  }
  if (mimeType.startsWith("audio/")) {
    return "audio";
  }
  if (mimeType === "application/pdf" || name.endsWith(".pdf")) {
    return "pdf";
  }
  if (
    mimeType.includes("markdown") ||
    name.endsWith(".md") ||
    name.endsWith(".markdown")
  ) {
    return "markdown";
  }
  if (
    mimeType.startsWith("application/vnd.openxmlformats-officedocument.") ||
    isOpenXmlOfficeExtension(name)
  ) {
    return null;
  }
  if (
    mimeType.startsWith("text/") ||
    mimeType.includes("json") ||
    mimeType === "application/xml" ||
    mimeType.endsWith("+xml") ||
    mimeType.includes("javascript") ||
    mimeType.includes("sql") ||
    name.endsWith(".log")
  ) {
    return "text";
  }

  return null;
}

function isOpenXmlOfficeExtension(name: string) {
  return Array.from(openXmlOfficeExtensions).some((extension) =>
    name.endsWith(extension),
  );
}

function findFileEntry(
  entries: ExplorerEntry[],
  entryPath: string | null,
): ExplorerFileEntry | null {
  if (!entryPath) {
    return null;
  }

  const entry = entries.find(
    (candidate) => candidate.type === "file" && candidate.path === entryPath,
  );

  return entry?.type === "file" ? entry : null;
}

function findDirectoryEntry(
  entries: ExplorerEntry[],
  entryPath: string | null,
): ExplorerDirectoryEntry | null {
  if (!entryPath) {
    return null;
  }

  const entry = entries.find(
    (candidate) =>
      candidate.type === "directory" && candidate.path === entryPath,
  );

  return entry?.type === "directory" ? entry : null;
}

function DeleteFolderDialog({
  bucket,
  entry,
  onOpenChange,
  onDeleteFolder,
}: {
  bucket: string;
  entry: ExplorerDirectoryEntry | null;
  onOpenChange: (open: boolean) => void;
  onDeleteFolder: (folderPath: string) => Promise<void>;
}) {
  const { t } = useI18n();
  if (!entry) {
    return null;
  }

  return (
    <AlertDialog onOpenChange={onOpenChange} open>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogMedia>
            <ShieldIcon />
          </AlertDialogMedia>
          <AlertDialogTitle>
            {t("explorer.deleteFolder.title")}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t("explorer.deleteFolder.description", {
              bucket,
              name: entry.name,
            })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => void onDeleteFolder(entry.path)}
            variant="destructive"
          >
            {t("common.delete")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function DownloadFolderZipButton({
  downloadingFolderPath,
  entry,
  onDownloadFolder,
}: {
  downloadingFolderPath: string;
  entry: ExplorerDirectoryEntry;
  onDownloadFolder: (folderPath: string) => Promise<void>;
}) {
  const { t } = useI18n();
  const pending = downloadingFolderPath === entry.path;

  return (
    <ExplorerIconButton
      disabled={pending}
      label={
        pending
          ? t("explorer.actions.downloadingFolderZip")
          : t("explorer.actions.downloadFolderZip")
      }
      onClick={() => {
        void onDownloadFolder(entry.path).catch(() => undefined);
      }}
    >
      {pending ? (
        <SyncIcon className="animate-spin text-sky-500" />
      ) : (
        <DownloadIcon className="text-sky-500" />
      )}
    </ExplorerIconButton>
  );
}

function PublishFolderSiteButton({
  entry,
  onOpenPublishSite,
  publishingPath,
}: {
  entry: ExplorerDirectoryEntry;
  onOpenPublishSite: (entryPath: string) => void;
  publishingPath: string;
}) {
  const { t } = useI18n();
  const pending = publishingPath === entry.path;
  const label = t("explorer.actions.publishSite");

  return (
    <ExplorerIconButton
      disabled={pending}
      label={label}
      onClick={() => onOpenPublishSite(entry.path)}
    >
      {pending ? (
        <SyncIcon className="animate-spin text-emerald-500" />
      ) : (
        <GlobeIcon className="text-emerald-500" />
      )}
    </ExplorerIconButton>
  );
}

function PublishObjectSiteButton({
  entry,
  onOpenPublishObjectSite,
  publishingPath,
}: {
  entry: ExplorerFileEntry;
  onOpenPublishObjectSite: (entryPath: string) => void;
  publishingPath: string;
}) {
  const { t } = useI18n();
  const pending = publishingPath === entry.path;
  const label = t("explorer.actions.publishSite");

  return (
    <ExplorerIconButton
      disabled={pending}
      label={label}
      onClick={() => onOpenPublishObjectSite(entry.path)}
    >
      {pending ? (
        <SyncIcon className="animate-spin text-emerald-500" />
      ) : (
        <GlobeIcon className="text-emerald-500" />
      )}
    </ExplorerIconButton>
  );
}

function DeleteFileDialog({
  bucket,
  entry,
  onOpenChange,
  onDeleteFile,
}: {
  bucket: string;
  entry: ExplorerFileEntry | null;
  onOpenChange: (open: boolean) => void;
  onDeleteFile: (objectKey: string) => Promise<void>;
}) {
  const { t } = useI18n();
  if (!entry) {
    return null;
  }

  return (
    <AlertDialog onOpenChange={onOpenChange} open>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogMedia>
            <ShieldIcon />
          </AlertDialogMedia>
          <AlertDialogTitle>{t("objects.delete.title")}</AlertDialogTitle>
          <AlertDialogDescription>
            {t("objects.delete.description", {
              bucket,
              objectKey: entry.object_key,
            })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => void onDeleteFile(entry.object_key)}
            variant="destructive"
          >
            {t("common.delete")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

const ExplorerIconButton = forwardRef<
  HTMLButtonElement,
  {
    children: React.ReactNode;
    disabled?: boolean;
    label: string;
    onClick?: () => void;
  }
>(function ExplorerIconButton({ children, disabled, label, onClick }, ref) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex">
          <ExplorerIconActionButton
            ref={ref}
            disabled={disabled}
            label={label}
            onClick={onClick}
          >
            {children}
          </ExplorerIconActionButton>
        </span>
      </TooltipTrigger>
      <TooltipContent className="whitespace-nowrap leading-none" sideOffset={6}>
        {label}
      </TooltipContent>
    </Tooltip>
  );
});

const ExplorerIconActionButton = forwardRef<
  HTMLButtonElement,
  {
    children: React.ReactNode;
    disabled?: boolean;
    label: string;
    onClick?: () => void;
  }
>(function ExplorerIconActionButton(
  { children, disabled, label, onClick },
  ref,
) {
  return (
    <Button
      ref={ref}
      className="[&_svg]:size-4"
      disabled={disabled}
      onClick={onClick}
      size="icon-sm"
      type="button"
      variant="ghost"
    >
      {children}
      <span className="sr-only">{label}</span>
    </Button>
  );
});
