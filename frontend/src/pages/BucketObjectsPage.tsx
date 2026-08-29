import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Fragment, useEffect, useState } from "react";
import {
  ChevronLeftIcon,
  ChevronRightIcon,
  CircleAlertIcon,
  DownloadIcon,
  FileTextIcon,
  FolderIcon,
  FolderSearchIcon,
  LoaderCircleIcon,
  RefreshCcwIcon,
  SearchIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { toast } from "sonner";
import { useParams, useSearchParams } from "react-router-dom";
import {
  buildApiURL,
  buildPublicObjectURL,
  checkObjectExists,
  createFolder,
  createSignedDownloadPath,
  deleteExplorerEntriesBatch,
  deleteFolder,
  deleteObject,
  downloadFolderZip,
  listExplorerEntries,
  uploadFolder,
  updateObjectVisibility,
  uploadObject,
} from "@/api/objects";
import {
  createSite,
  publishObjectSite,
  uploadFileAndPublishSite,
  uploadAndPublishSite,
} from "@/api/sites";
import type {
  DeleteExplorerEntriesBatchFailedItem,
  ExplorerDirectoryEntry,
  ExplorerFileEntry,
  PublishSiteResult,
} from "@/api/types";
import { EmptyState } from "@/components/EmptyState";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Spinner } from "@/components/ui/spinner";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { CreateFolderDialog } from "@/features/explorer/CreateFolderDialog";
import { ExplorerTable } from "@/features/explorer/ExplorerTable";
import { type PublishSiteValue } from "@/features/explorer/PublishSiteDialog";
import {
  UploadFolderDialog,
  type UploadFolderDialogValue,
} from "@/features/explorer/UploadFolderDialog";
import { PublishObjectSiteValue } from "@/features/explorer/PublishObjectSiteDialog";
import {
  UploadAndPublishSiteDialog,
  type UploadAndPublishSiteValue,
} from "@/features/sites/UploadAndPublishSiteDialog";
import {
  UploadObjectDialog,
  type UploadDialogValue,
} from "@/features/explorer/UploadObjectDialog";
import { RecycleBinDialog } from "@/features/buckets/RecycleBinDialog";
import {
  explorerPageSizes,
  getExplorerBreadcrumbs,
  getParentExplorerPrefix,
  normalizeExplorerPrefix,
  normalizeExplorerSearch,
  normalizeExplorerSortBy,
  normalizeExplorerSortOrder,
  parseExplorerLimit,
  joinExplorerPath,
  type ExplorerSortBy,
  type ExplorerSortOrder,
} from "@/lib/explorer";
import { useI18n } from "@/lib/i18n";
import { useAppSettings } from "@/lib/settings";
import { cn, downloadFile } from "@/lib/utils";

type PendingOverwriteUpload = {
  kind: "object";
  value: UploadDialogValue;
  target?: string;
};

function ExplorerEntriesLoadingOverlay({
  absolute = false,
}: {
  absolute?: boolean;
}) {
  const content = (
    <div className="flex items-center justify-center rounded-md bg-background/80 p-3 text-muted-foreground shadow-sm ring-1 ring-border/70">
      <Spinner className="size-5" />
    </div>
  );

  if (absolute) {
    return (
      <div className="absolute inset-0 flex items-center justify-center bg-card/70 backdrop-blur-[1px]">
        {content}
      </div>
    );
  }

  return (
    <div className="flex min-h-[320px] flex-1 items-center justify-center p-6">
      {content}
    </div>
  );
}

export function BucketObjectsPage() {
  const { bucket = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const { settings } = useAppSettings();
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [searchInput, setSearchInput] = useState("");
  const [cursorHistory, setCursorHistory] = useState<string[]>([]);
  const [prefixBackHistory, setPrefixBackHistory] = useState<string[]>([]);
  const [prefixForwardHistory, setPrefixForwardHistory] = useState<string[]>(
    [],
  );
  const [uploadProgress, setUploadProgress] = useState(0);
  const [uploadDialogResetSignal, setUploadDialogResetSignal] = useState(0);
  const [isCheckingOverwrite, setIsCheckingOverwrite] = useState(false);
  const [pendingOverwriteUpload, setPendingOverwriteUpload] =
    useState<PendingOverwriteUpload | null>(null);
  const [isRetryingOverwrite, setIsRetryingOverwrite] = useState(false);
  const [deletingPath, setDeletingPath] = useState("");
  const [downloadingFilePath, setDownloadingFilePath] = useState("");
  const [downloadingFolderPath, setDownloadingFolderPath] = useState("");
  const [selectedPaths, setSelectedPaths] = useState<Set<string>>(
    () => new Set(),
  );
  const [bulkDownloadPending, setBulkDownloadPending] = useState(false);
  const [bulkDeletePending, setBulkDeletePending] = useState(false);
  const [bulkDeleteDialogOpen, setBulkDeleteDialogOpen] = useState(false);
  const [publishingPath, setPublishingPath] = useState("");

  const prefix = normalizeExplorerPrefix(searchParams.get("prefix"));
  const search = normalizeExplorerSearch(searchParams.get("search"));
  const cursor = searchParams.get("cursor") ?? "";
  const limit = parseExplorerLimit(searchParams.get("limit"));
  const sortBy =
    normalizeExplorerSortBy(searchParams.get("sort_by")) ?? "created_at";
  const sortOrder =
    normalizeExplorerSortOrder(searchParams.get("sort_order")) ?? "desc";
  const entriesBaseQueryKey = [
    "explorer-entries",
    settings.apiBaseUrl,
    settings.bearerToken,
    bucket,
  ] as const;
  const sitesQueryKey = [
    "sites",
    settings.apiBaseUrl,
    settings.bearerToken,
  ] as const;
  const entriesQueryKey = [
    ...entriesBaseQueryKey,
    prefix,
    search,
    cursor,
    limit,
    sortBy,
    sortOrder,
  ] as const;

  useEffect(() => {
    setSearchInput(search);
  }, [search]);

  useEffect(() => {
    setCursorHistory([]);
  }, [bucket, prefix, search, limit, sortBy, sortOrder]);

  useEffect(() => {
    setPrefixBackHistory([]);
    setPrefixForwardHistory([]);
  }, [bucket]);

  useEffect(() => {
    setSelectedPaths(new Set());
  }, [bucket, prefix, search, cursor, limit, sortBy, sortOrder]);

  const entriesQuery = useQuery({
    queryKey: entriesQueryKey,
    queryFn: () =>
      listExplorerEntries(settings, {
        bucket,
        prefix,
        search,
        limit,
        cursor,
        sortBy,
        sortOrder,
      }),
    enabled: bucket !== "",
    placeholderData: keepPreviousData,
  });

  const entryItems = entriesQuery.data?.items;
  const entries = entryItems ?? [];
  const selectedEntries = entries.filter((entry) =>
    selectedPaths.has(entry.path),
  );
  const selectedCount = selectedEntries.length;
  const bulkActionsPending = bulkDownloadPending || bulkDeletePending;

  useEffect(() => {
    setSelectedPaths((current) => {
      const visiblePaths = new Set(
        (entryItems ?? []).map((entry) => entry.path),
      );
      let changed = false;
      const next = new Set<string>();

      current.forEach((path) => {
        if (visiblePaths.has(path)) {
          next.add(path);
          return;
        }

        changed = true;
      });

      return changed ? next : current;
    });
  }, [entryItems]);

  function uploadObjectRequest(
    value: UploadDialogValue,
    allowOverwrite: boolean,
  ) {
    return uploadObject(settings, {
      bucket,
      objectKey: joinExplorerPath(prefix, value.objectKey),
      file: value.file,
      visibility: value.visibility,
      allowOverwrite,
      onProgress: setUploadProgress,
    });
  }

  function uploadFolderRequest(
    value: UploadFolderDialogValue,
    allowOverwrite: boolean,
  ) {
    return uploadFolder(settings, {
      bucket,
      prefix,
      files: value.files,
      visibility: value.visibility,
      allowOverwrite,
      onProgress: setUploadProgress,
    });
  }

  async function findObjectConflictTarget(value: UploadDialogValue) {
    const objectKey = joinExplorerPath(prefix, value.objectKey);
    const exists = await checkObjectExists(settings, bucket, objectKey);
    return exists ? objectKey : null;
  }

  const uploadMutation = useMutation({
    mutationFn: (value: UploadDialogValue) => uploadObjectRequest(value, false),
    onSuccess: async () => {
      setUploadProgress(0);
      toast.success(t("toast.objectUploaded"));
      await queryClient.invalidateQueries({ queryKey: entriesBaseQueryKey });
    },
    onError: (error, value) => {
      setUploadProgress(0);
      if (isObjectExistsError(error)) {
        setPendingOverwriteUpload({
          kind: "object",
          value,
          target: joinExplorerPath(prefix, value.objectKey),
        });
        return;
      }

      const message =
        error instanceof Error ? error.message : t("errors.uploadObject");
      toast.error(message);
    },
  });

  const uploadFolderMutation = useMutation({
    mutationFn: (value: UploadFolderDialogValue) =>
      uploadFolderRequest(value, true),
    onSuccess: async () => {
      setUploadProgress(0);
      toast.success(t("toast.folderUploaded"));
      await queryClient.invalidateQueries({ queryKey: entriesBaseQueryKey });
    },
    onError: (error) => {
      setUploadProgress(0);
      const message =
        error instanceof Error ? error.message : t("errors.uploadFolder");
      toast.error(message);
    },
  });

  const createFolderMutation = useMutation({
    mutationFn: (name: string) =>
      createFolder(settings, {
        bucket,
        prefix,
        name,
      }),
    onSuccess: async () => {
      toast.success(t("toast.folderCreated"));
      await queryClient.invalidateQueries({ queryKey: entriesBaseQueryKey });
    },
    onError: (error) => {
      const message =
        error instanceof Error ? error.message : t("errors.createFolder");
      toast.error(message);
    },
  });

  const uploadAndPublishSiteMutation = useMutation({
    mutationFn: async (
      value: UploadAndPublishSiteValue,
    ): Promise<PublishSiteResult> => {
      if (value.mode === "folder") {
        return uploadAndPublishSite(settings, {
          bucket: value.bucket,
          parentPrefix: value.parentPrefix,
          files: value.files,
          domains: value.domains,
          enabled: value.enabled,
          indexDocument: value.indexDocument,
          errorDocument: value.errorDocument,
          spaFallback: value.spaFallback,
          onProgress: setUploadProgress,
        });
      }

      const site = await uploadFileAndPublishSite(settings, {
        bucket: value.bucket,
        parentPrefix: value.parentPrefix,
        file: value.file,
        domains: value.domains,
        enabled: value.enabled,
        errorDocument: value.errorDocument,
        spaFallback: value.spaFallback,
        onProgress: setUploadProgress,
      });

      return {
        uploaded_count: 1,
        site,
      };
    },
    onSuccess: async () => {
      setUploadProgress(0);
      toast.success(t("toast.sitePublished"));
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: entriesBaseQueryKey }),
        queryClient.invalidateQueries({ queryKey: sitesQueryKey }),
      ]);
    },
    onError: (error) => {
      setUploadProgress(0);
      const message =
        error instanceof Error ? error.message : t("errors.publishSite");
      toast.error(message);
    },
  });

  const deleteFileMutation = useMutation({
    mutationFn: async (objectKey: string) => {
      setDeletingPath(objectKey);
      await deleteObject(settings, bucket, objectKey);
    },
    onSuccess: async () => {
      toast.success(t("toast.objectDeleted"));
      await queryClient.invalidateQueries({
        queryKey: entriesBaseQueryKey,
      });
    },
    onError: (error) => {
      const message =
        error instanceof Error ? error.message : t("errors.deleteObject");
      toast.error(message);
    },
    onSettled: () => {
      setDeletingPath("");
    },
  });

  const deleteFolderMutation = useMutation({
    mutationFn: async (folderPath: string) => {
      setDeletingPath(folderPath);
      await deleteFolder(settings, bucket, folderPath, { recursive: true });
    },
    onSuccess: async () => {
      toast.success(t("toast.folderDeleted"));
      await queryClient.invalidateQueries({ queryKey: entriesBaseQueryKey });
    },
    onError: (error) => {
      const message =
        error instanceof Error ? error.message : t("errors.deleteFolder");
      toast.error(message);
    },
    onSettled: () => {
      setDeletingPath("");
    },
  });

  const updateVisibilityMutation = useMutation({
    mutationFn: (input: {
      objectKey: string;
      visibility: "public" | "private";
    }) =>
      updateObjectVisibility(settings, {
        bucket,
        objectKey: input.objectKey,
        visibility: input.visibility,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: entriesBaseQueryKey });
    },
  });

  const publishSiteMutation = useMutation({
    mutationFn: async (input: {
      folderPath: string;
      value: PublishSiteValue;
    }) => {
      setPublishingPath(input.folderPath);
      return createSite(settings, {
        bucket,
        root_prefix: input.folderPath,
        enabled: input.value.enabled,
        index_document: input.value.indexDocument,
        error_document: input.value.errorDocument,
        spa_fallback: input.value.spaFallback,
        domains: input.value.domains,
      });
    },
    onSuccess: () => {
      toast.success(t("toast.sitePublished"));
    },
    onError: (error) => {
      const message =
        error instanceof Error ? error.message : t("errors.publishSite");
      toast.error(message);
    },
    onSettled: () => {
      setPublishingPath("");
    },
  });

  const publishObjectSiteMutation = useMutation({
    mutationFn: async (input: {
      objectKey: string;
      value: PublishObjectSiteValue;
    }) => {
      setPublishingPath(input.objectKey);
      return publishObjectSite(settings, {
        bucket,
        objectKey: input.objectKey,
        domains: input.value.domains,
        enabled: input.value.enabled,
        errorDocument: input.value.errorDocument,
        spaFallback: input.value.spaFallback,
      });
    },
    onSuccess: async () => {
      toast.success(t("toast.sitePublished"));
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: entriesBaseQueryKey }),
        queryClient.invalidateQueries({ queryKey: sitesQueryKey }),
      ]);
    },
    onError: (error) => {
      const message =
        error instanceof Error ? error.message : t("errors.publishSite");
      toast.error(message);
    },
    onSettled: () => {
      setPublishingPath("");
    },
  });

  const breadcrumbs = getExplorerBreadcrumbs(prefix);
  const uploadPending =
    uploadMutation.isPending ||
    uploadFolderMutation.isPending ||
    uploadAndPublishSiteMutation.isPending ||
    isCheckingOverwrite ||
    isRetryingOverwrite;
  const entriesLoading = entriesQuery.isFetching;
  const hasEntriesData = entriesQuery.data !== undefined;
  const showEntriesInitialLoading = !hasEntriesData && entriesLoading;
  const showEntriesLoadingOverlay = hasEntriesData && entriesLoading;
  const bucketMissing = isBucketNotFoundError(entriesQuery.error);
  const overwriteTarget = pendingOverwriteUpload
    ? (pendingOverwriteUpload.target ??
      joinExplorerPath(
        prefix,
        pendingOverwriteUpload.value.objectKey ||
          pendingOverwriteUpload.value.file.name,
      ))
    : "";

  if (!bucket) {
    return (
      <EmptyState
        description={t("errors.bucketNotFound")}
        icon={FolderSearchIcon}
        title={t("explorer.title")}
      />
    );
  }

  if (bucketMissing) {
    return (
      <EmptyState
        description={t("errors.bucketNotFound")}
        icon={FolderSearchIcon}
        title={t("explorer.title")}
      />
    );
  }

  function updateSearchParams(
    updates: Partial<
      Record<
        "prefix" | "search" | "cursor" | "limit" | "sort_by" | "sort_order",
        string
      >
    >,
  ) {
    const next = new URLSearchParams(searchParams);

    for (const [key, value] of Object.entries(updates)) {
      if (!value) {
        next.delete(key);
        continue;
      }
      next.set(key, value);
    }

    setSearchParams(next, { replace: false });
  }

  function applyPrefixNavigation(nextPrefix: string) {
    setCursorHistory([]);
    setSearchInput("");
    updateSearchParams({
      prefix: nextPrefix || "",
      search: "",
      cursor: "",
    });
  }

  function handleNavigatePrefix(
    nextPrefix: string,
    options?: {
      clearForwardHistory?: boolean;
      recordHistory?: boolean;
    },
  ) {
    const normalizedNextPrefix = normalizeExplorerPrefix(nextPrefix);
    const recordHistory = options?.recordHistory ?? true;
    const clearForwardHistory = options?.clearForwardHistory ?? true;

    if (normalizedNextPrefix !== prefix) {
      if (recordHistory) {
        setPrefixBackHistory((history) => [...history, prefix]);
      }
      if (clearForwardHistory) {
        setPrefixForwardHistory([]);
      }
    }

    applyPrefixNavigation(normalizedNextPrefix);
  }

  function handlePrefixBack() {
    if (prefixBackHistory.length > 0) {
      const previousPrefix =
        prefixBackHistory[prefixBackHistory.length - 1] ?? "";
      setPrefixBackHistory((history) => history.slice(0, -1));
      setPrefixForwardHistory((history) => [...history, prefix]);
      applyPrefixNavigation(previousPrefix);
      return;
    }

    const parentPrefix = getParentExplorerPrefix(prefix);
    if (parentPrefix === prefix) {
      return;
    }

    setPrefixForwardHistory((history) => [...history, prefix]);
    applyPrefixNavigation(parentPrefix);
  }

  function handlePrefixForward() {
    if (prefixForwardHistory.length === 0) {
      return;
    }

    const nextPrefix =
      prefixForwardHistory[prefixForwardHistory.length - 1] ?? "";
    setPrefixForwardHistory((history) => history.slice(0, -1));
    setPrefixBackHistory((history) => [...history, prefix]);
    applyPrefixNavigation(nextPrefix);
  }

  const canNavigatePrefixBack = prefixBackHistory.length > 0 || prefix !== "";
  const canNavigatePrefixForward = prefixForwardHistory.length > 0;

  async function handleUpload(value: UploadDialogValue) {
    setUploadProgress(0);

    let conflictTarget: string | null = null;
    setIsCheckingOverwrite(true);
    try {
      conflictTarget = await findObjectConflictTarget(value);
    } catch (error) {
      const message =
        error instanceof Error ? error.message : t("errors.uploadObject");
      toast.error(message);
      return;
    } finally {
      setIsCheckingOverwrite(false);
    }

    if (conflictTarget) {
      setPendingOverwriteUpload({
        kind: "object",
        value,
        target: conflictTarget,
      });
      throw new Error("overwrite_confirmation_required");
    }

    await uploadMutation.mutateAsync(value);
  }

  async function handleUploadFolder(value: UploadFolderDialogValue) {
    setUploadProgress(0);
    await uploadFolderMutation.mutateAsync(value);
  }

  async function handleConfirmOverwriteUpload() {
    if (!pendingOverwriteUpload || isRetryingOverwrite) {
      return;
    }

    const overwriteUpload = pendingOverwriteUpload;
    setPendingOverwriteUpload(null);
    setIsRetryingOverwrite(true);
    try {
      await uploadObjectRequest(overwriteUpload.value, true);
      setUploadDialogResetSignal((value) => value + 1);
      toast.success(t("toast.objectUploaded"));

      await queryClient.invalidateQueries({ queryKey: entriesBaseQueryKey });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : t("errors.uploadObject");
      toast.error(message);
    } finally {
      setIsRetryingOverwrite(false);
      setUploadProgress(0);
    }
  }

  function handleCancelOverwriteUpload() {
    if (isRetryingOverwrite) {
      return;
    }

    setPendingOverwriteUpload(null);
    setUploadProgress(0);
  }

  async function handleCreateFolder(name: string) {
    await createFolderMutation.mutateAsync(name);
  }

  async function handleUploadAndPublishSite(value: UploadAndPublishSiteValue) {
    await uploadAndPublishSiteMutation.mutateAsync(value);
  }

  async function handleDeleteFile(objectKey: string) {
    await deleteFileMutation.mutateAsync(objectKey);
  }

  async function handleDeleteFolder(folderPath: string) {
    await deleteFolderMutation.mutateAsync(folderPath);
  }

  function handleSelectAll(checked: boolean | "indeterminate") {
    if (bulkActionsPending) {
      return;
    }

    setSelectedPaths(
      checked === true
        ? new Set(entries.map((entry) => entry.path))
        : new Set(),
    );
  }

  function handleSelectEntry(
    entryPath: string,
    checked: boolean | "indeterminate",
  ) {
    if (bulkActionsPending) {
      return;
    }

    setSelectedPaths((current) => {
      const next = new Set(current);

      if (checked === true) {
        next.add(entryPath);
      } else {
        next.delete(entryPath);
      }

      return next;
    });
  }

  async function downloadExplorerFileEntry(entry: ExplorerFileEntry) {
    const downloadUrl =
      entry.visibility === "public"
        ? buildPublicObjectURL(settings.apiBaseUrl, bucket, entry.object_key, {
            download: true,
          })
        : buildApiURL(
            settings.apiBaseUrl,
            (await createSignedDownloadPath(settings, bucket, entry.object_key))
              .path,
          );

    await downloadFile(
      appendDownloadQuery(downloadUrl),
      entry.original_filename || entry.name,
    );
  }

  async function downloadExplorerDirectoryEntry(entry: ExplorerDirectoryEntry) {
    await downloadFolderZip(settings, bucket, entry.path);
  }

  async function handleDownloadFile(entry: ExplorerFileEntry) {
    setDownloadingFilePath(entry.path);
    try {
      await downloadExplorerFileEntry(entry);
    } catch (error) {
      const message =
        error instanceof Error ? error.message : t("errors.downloadObject");
      toast.error(message);
    } finally {
      setDownloadingFilePath("");
    }
  }

  async function handleDownloadFolder(folderPath: string) {
    setDownloadingFolderPath(folderPath);
    try {
      await downloadFolderZip(settings, bucket, folderPath);
    } catch (error) {
      const message =
        error instanceof Error ? error.message : t("errors.downloadFolderZip");
      toast.error(message);
    } finally {
      setDownloadingFolderPath("");
    }
  }

  function appendDownloadQuery(url: string) {
    try {
      const next = new URL(url);
      next.searchParams.set("download", "true");
      return next.toString();
    } catch {
      return `${url}${url.includes("?") ? "&" : "?"}download=true`;
    }
  }

  async function handlePublishSite(
    folderPath: string,
    value: PublishSiteValue,
  ) {
    await publishSiteMutation.mutateAsync({ folderPath, value });
  }

  async function handlePublishObjectSite(
    objectKey: string,
    value: PublishObjectSiteValue,
  ) {
    await publishObjectSiteMutation.mutateAsync({ objectKey, value });
  }

  async function handleUpdateVisibility(
    objectKey: string,
    visibility: "public" | "private",
  ) {
    await updateVisibilityMutation.mutateAsync({ objectKey, visibility });
  }

  async function handleBulkDownload() {
    if (selectedEntries.length === 0 || bulkActionsPending) {
      return;
    }

    let successCount = 0;
    let failedCount = 0;
    setBulkDownloadPending(true);

    try {
      for (const entry of selectedEntries) {
        try {
          if (entry.type === "file") {
            setDownloadingFilePath(entry.path);
            await downloadExplorerFileEntry(entry);
          } else {
            setDownloadingFolderPath(entry.path);
            await downloadExplorerDirectoryEntry(entry);
          }
          successCount++;
        } catch {
          failedCount++;
        } finally {
          setDownloadingFilePath("");
          setDownloadingFolderPath("");
        }
      }
    } finally {
      setBulkDownloadPending(false);
    }

    if (failedCount === 0) {
      toast.success(
        t("toast.bulkDownloadCompleted", {
          count: successCount,
        }),
      );
      return;
    }

    toast.error(
      t("toast.bulkDownloadPartial", {
        successCount,
        failedCount,
      }),
    );
  }

  async function handleConfirmBulkDelete() {
    if (selectedEntries.length === 0 || bulkDeletePending) {
      return;
    }

    setBulkDeletePending(true);
    try {
      const result = await deleteExplorerEntriesBatch(
        settings,
        bucket,
        selectedEntries.map((entry) => ({
          type: entry.type,
          path: entry.path,
        })),
      );

      const failedPaths = new Set(
        result.failed_items.map(
          (item: DeleteExplorerEntriesBatchFailedItem) => item.path,
        ),
      );

      if (result.failed_count === 0) {
        setSelectedPaths(new Set());
        toast.success(
          t("toast.bulkDeleteCompleted", {
            count: result.deleted_count,
          }),
        );
      } else {
        setSelectedPaths(failedPaths);
        toast.error(
          t("toast.bulkDeletePartial", {
            successCount: result.deleted_count,
            failedCount: result.failed_count,
          }),
        );
      }

      setBulkDeleteDialogOpen(false);
      await queryClient.invalidateQueries({ queryKey: entriesBaseQueryKey });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : t("errors.bulkDeleteEntries");
      toast.error(message);
    } finally {
      setBulkDeletePending(false);
    }
  }

  function handleSearchSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setCursorHistory([]);
    updateSearchParams({
      search: searchInput.trim(),
      cursor: "",
    });
  }

  function handleSortApply(
    nextSortBy: ExplorerSortBy,
    nextSortOrder: ExplorerSortOrder,
  ) {
    setCursorHistory([]);
    updateSearchParams({
      sort_by: nextSortBy,
      sort_order: nextSortOrder,
      cursor: "",
    });
  }

  function handleSortClear() {
    setCursorHistory([]);
    updateSearchParams({
      sort_by: "",
      sort_order: "",
      cursor: "",
    });
  }

  function handleRefreshEntries() {
    void entriesQuery.refetch();
  }

  function handleNextPage() {
    if (!entriesQuery.data?.next_cursor) {
      return;
    }

    setCursorHistory((history) => [...history, cursor]);
    updateSearchParams({
      cursor: entriesQuery.data.next_cursor,
    });
  }

  function handlePrevPage() {
    if (cursorHistory.length === 0) {
      return;
    }

    const nextHistory = [...cursorHistory];
    const previousCursor = nextHistory.pop() ?? "";
    setCursorHistory(nextHistory);
    updateSearchParams({
      cursor: previousCursor,
    });
  }

  const bulkActionButtons = [
    {
      key: "download",
      disabled: bulkActionsPending,
      label: bulkDownloadPending
        ? t("explorer.bulk.downloading")
        : t("explorer.bulk.download"),
      onClick: () => {
        void handleBulkDownload();
      },
      variant: "outline" as const,
      icon: bulkDownloadPending ? (
        <LoaderCircleIcon className="animate-spin" />
      ) : (
        <DownloadIcon />
      ),
    },
    {
      key: "delete",
      disabled: bulkActionsPending,
      label: bulkDeletePending
        ? t("explorer.bulk.deleting")
        : t("explorer.bulk.delete"),
      onClick: () => setBulkDeleteDialogOpen(true),
      variant: "destructive" as const,
      icon: bulkDeletePending ? (
        <LoaderCircleIcon className="animate-spin" />
      ) : (
        <Trash2Icon />
      ),
    },
    {
      key: "clear",
      disabled: bulkActionsPending,
      label: t("explorer.bulk.clearSelection"),
      onClick: () => setSelectedPaths(new Set()),
      variant: "ghost" as const,
      icon: <XIcon />,
    },
  ];

  return (
    <section className="flex min-h-0 flex-1 flex-col gap-6">
      <Card className="flex min-h-[720px] flex-1 flex-col overflow-hidden border-border/70 bg-card py-0 gap-0">
        <div className="border-b border-border/70 px-4 py-3">
          <div className="flex flex-wrap items-center gap-3">
            <div className="flex items-center gap-2">
              <Button
                disabled={!canNavigatePrefixBack}
                onClick={handlePrefixBack}
                size="icon-sm"
                type="button"
                variant="outline"
              >
                <ChevronLeftIcon />
                <span className="sr-only">{t("explorer.actions.goBack")}</span>
              </Button>
              <Button
                disabled={!canNavigatePrefixForward}
                onClick={handlePrefixForward}
                size="icon-sm"
                type="button"
                variant="outline"
              >
                <ChevronRightIcon />
                <span className="sr-only">
                  {t("explorer.actions.goForward")}
                </span>
              </Button>
            </div>

            <Separator className="hidden h-5 sm:block" orientation="vertical" />

            <div className="min-w-0 flex-1">
              <Breadcrumb>
                <BreadcrumbList>
                  <BreadcrumbItem>
                    <BreadcrumbLink asChild>
                      <button
                        onClick={() => handleNavigatePrefix("")}
                        type="button"
                      >
                        {bucket}
                      </button>
                    </BreadcrumbLink>
                  </BreadcrumbItem>
                  {breadcrumbs.map((item, index) => {
                    const isLast = index === breadcrumbs.length - 1;
                    return (
                      <Fragment key={item.prefix}>
                        <BreadcrumbSeparator />
                        <BreadcrumbItem>
                          {isLast ? (
                            <BreadcrumbPage>{item.label}</BreadcrumbPage>
                          ) : (
                            <BreadcrumbLink asChild>
                              <button
                                onClick={() =>
                                  handleNavigatePrefix(item.prefix)
                                }
                                type="button"
                              >
                                {item.label}
                              </button>
                            </BreadcrumbLink>
                          )}
                        </BreadcrumbItem>
                      </Fragment>
                    );
                  })}
                </BreadcrumbList>
              </Breadcrumb>
            </div>
          </div>
        </div>

        <div className="flex min-h-0 flex-1 flex-col">
          <div className="flex min-h-0 flex-1 flex-col min-w-0">
            <div className="border-b border-border/70 px-4 py-3">
              <div className="flex flex-col gap-4 lg:flex-row lg:items-start">
                <div className="flex flex-wrap items-center gap-2">
                  <UploadObjectDialog
                    currentPrefix={prefix}
                    onSubmit={handleUpload}
                    pending={uploadPending}
                    progress={uploadProgress}
                    resetSignal={uploadDialogResetSignal}
                  />

                  <UploadFolderDialog
                    currentPrefix={prefix}
                    onSubmit={handleUploadFolder}
                    pending={uploadPending}
                    progress={uploadProgress}
                  />

                  <Separator
                    className="hidden h-5 sm:block"
                    orientation="vertical"
                  />

                  <UploadAndPublishSiteDialog
                    bucket={bucket}
                    lockedFields={{ bucket: true, parentPrefix: true }}
                    onSubmit={handleUploadAndPublishSite}
                    parentPrefix={prefix}
                    pending={uploadAndPublishSiteMutation.isPending}
                    progress={uploadProgress}
                  />

                  <CreateFolderDialog
                    currentPrefix={prefix}
                    onSubmit={handleCreateFolder}
                    pending={createFolderMutation.isPending}
                  />

                  <Separator
                    className="hidden h-5 sm:block"
                    orientation="vertical"
                  />

                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Button
                        aria-label={t("explorer.toolbar.refresh")}
                        onClick={handleRefreshEntries}
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                      >
                        <RefreshCcwIcon />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent
                      className="whitespace-nowrap leading-none"
                      sideOffset={6}
                    >
                      {t("explorer.toolbar.refresh")}
                    </TooltipContent>
                  </Tooltip>
                </div>

                <div className="flex w-full min-w-0 flex-col gap-3 lg:ml-auto lg:max-w-xl">
                  <div className="flex w-full min-w-0 items-center gap-2">
                    <RecycleBinDialog bucketName={bucket} />
                    <form
                      className="flex min-w-0 flex-1 items-center gap-2"
                      onSubmit={handleSearchSubmit}
                    >
                      <FieldGroup className="min-w-0 flex-1">
                        <Field className="min-w-0" orientation="responsive">
                          <FieldLabel
                            className="sr-only"
                            htmlFor="explorer-search"
                          >
                            {t("explorer.toolbar.search")}
                          </FieldLabel>
                          <Input
                            className="min-w-0"
                            id="explorer-search"
                            onChange={(event) =>
                              setSearchInput(event.target.value)
                            }
                            placeholder={t(
                              "explorer.toolbar.searchPlaceholder",
                            )}
                            value={searchInput}
                          />
                        </Field>
                      </FieldGroup>
                      <Button
                        className="shrink-0"
                        size="icon"
                        type="submit"
                        variant="outline"
                      >
                        <SearchIcon />
                        <span className="sr-only">{t("common.apply")}</span>
                      </Button>
                    </form>
                  </div>
                </div>
              </div>
            </div>

            {entriesQuery.isError ? (
              <div className="border-b border-border/70 p-4">
                <Alert variant="destructive">
                  <CircleAlertIcon />
                  <AlertTitle>{t("errors.loadEntries")}</AlertTitle>
                  <AlertDescription>
                    {entriesQuery.error.message}
                  </AlertDescription>
                </Alert>
              </div>
            ) : null}

            <div className="relative flex min-h-0 flex-1 flex-col">
              {showEntriesInitialLoading ? (
                <ExplorerEntriesLoadingOverlay />
              ) : entries.length > 0 ? (
                <>
                  <div className="min-h-0 flex-1 overflow-auto">
                    <ExplorerTable
                      bucket={bucket}
                      buildPublicUrl={(objectKey) =>
                        buildPublicObjectURL(
                          settings.apiBaseUrl,
                          bucket,
                          objectKey,
                        )
                      }
                      deletingPath={deletingPath}
                      downloadingFilePath={downloadingFilePath}
                      downloadingFolderPath={downloadingFolderPath}
                      entries={entries}
                      onDeleteFile={handleDeleteFile}
                      onDeleteFolder={handleDeleteFolder}
                      onDownloadFile={handleDownloadFile}
                      onDownloadFolder={handleDownloadFolder}
                      onOpenDirectory={handleNavigatePrefix}
                      onPublishObjectSite={handlePublishObjectSite}
                      onPublishSite={handlePublishSite}
                      onSelectAll={handleSelectAll}
                      onSelectEntry={handleSelectEntry}
                      onSortApply={handleSortApply}
                      onSortClear={handleSortClear}
                      onUpdateVisibility={handleUpdateVisibility}
                      publishingPath={publishingPath}
                      selectedPaths={selectedPaths}
                      selectionDisabled={bulkActionsPending}
                      sortBy={sortBy}
                      sortOrder={sortOrder}
                    />
                  </div>
                  <div className="flex flex-col gap-4 border-t border-border/70 px-4 py-3 sm:flex-row sm:items-center">
                    {selectedCount > 0 ? (
                      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
                        <div className="mr-1 text-sm font-medium text-foreground">
                          {t("explorer.bulk.selectedCount", {
                            count: selectedCount,
                          })}
                        </div>
                        {bulkActionButtons.map((action) => (
                          <Button
                            key={action.key}
                            disabled={action.disabled}
                            onClick={action.onClick}
                            type="button"
                            variant={action.variant}
                          >
                            {action.icon}
                            {action.label}
                          </Button>
                        ))}
                      </div>
                    ) : null}
                    <div className="flex flex-wrap items-center gap-2 sm:ml-auto sm:justify-end">
                      <div className="text-sm text-muted-foreground">
                        {t("explorer.pagination.summary", {
                          count: entries.length,
                          page: cursorHistory.length + 1,
                        })}
                      </div>
                      <Select
                        onValueChange={(value) =>
                          updateSearchParams({
                            limit: value,
                            cursor: "",
                          })
                        }
                        value={String(limit)}
                      >
                        <SelectTrigger
                          aria-label={t("explorer.toolbar.limit")}
                          className="w-full sm:w-[140px]"
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent align="end" position="popper">
                          <SelectGroup>
                            {explorerPageSizes.map((size) => (
                              <SelectItem key={size} value={String(size)}>
                                {t("explorer.limit.option", { count: size })}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>

                      <div className="flex gap-2">
                        <Button
                          disabled={cursorHistory.length === 0}
                          onClick={handlePrevPage}
                          type="button"
                          variant="outline"
                        >
                          {t("objects.pagination.previous")}
                        </Button>
                        <Button
                          disabled={!entriesQuery.data?.next_cursor}
                          onClick={handleNextPage}
                          type="button"
                          variant="outline"
                        >
                          {t("objects.pagination.next")}
                        </Button>
                      </div>
                    </div>
                  </div>
                </>
              ) : (
                <div className="p-6">
                  <EmptyState
                    description={t("explorer.empty.description")}
                    icon={FolderSearchIcon}
                    title={t("explorer.empty.title")}
                  />
                </div>
              )}

              {showEntriesLoadingOverlay ? (
                <ExplorerEntriesLoadingOverlay absolute />
              ) : null}
            </div>
          </div>
        </div>
      </Card>

      <AlertDialog
        onOpenChange={(open) => {
          if (!bulkDeletePending) {
            setBulkDeleteDialogOpen(open);
          }
        }}
        open={bulkDeleteDialogOpen}
      >
        <AlertDialogContent
          className="overflow-hidden data-[size=sm]:max-w-[calc(100vw-1.5rem)] sm:data-[size=sm]:max-w-md"
          size="sm"
        >
          <AlertDialogHeader>
            <AlertDialogMedia>
              <CircleAlertIcon />
            </AlertDialogMedia>
            <AlertDialogTitle>
              {t("explorer.bulk.deleteConfirmTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("explorer.bulk.deleteConfirmDescription", {
                count: selectedCount,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>

          <ScrollArea className="max-h-56 w-full min-w-0 overflow-hidden rounded-lg border border-border/70">
            <ul className="flex min-w-0 flex-col divide-y divide-border/60">
              {selectedEntries.map((entry) => (
                <BulkDeletePreviewItem entry={entry} key={entry.path} />
              ))}
            </ul>
          </ScrollArea>

          <AlertDialogFooter>
            <AlertDialogCancel disabled={bulkDeletePending}>
              {t("common.cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={bulkDeletePending}
              onClick={(event) => {
                event.preventDefault();
                void handleConfirmBulkDelete();
              }}
              variant="destructive"
            >
              {bulkDeletePending ? (
                <LoaderCircleIcon
                  className="animate-spin"
                  data-icon="inline-start"
                />
              ) : null}
              {t("explorer.bulk.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        onOpenChange={(open) => {
          if (!open) {
            handleCancelOverwriteUpload();
          }
        }}
        open={pendingOverwriteUpload !== null}
      >
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogMedia>
              <CircleAlertIcon />
            </AlertDialogMedia>
            <AlertDialogTitle>{t("explorer.overwrite.title")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("explorer.overwrite.description", {
                target: overwriteTarget,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isRetryingOverwrite}>
              {t("common.cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={isRetryingOverwrite}
              onClick={(event) => {
                event.preventDefault();
                void handleConfirmOverwriteUpload();
              }}
            >
              {isRetryingOverwrite ? (
                <LoaderCircleIcon
                  className="animate-spin"
                  data-icon="inline-start"
                />
              ) : null}
              {t("explorer.overwrite.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

function BulkDeletePreviewItem({
  entry,
}: {
  entry: ExplorerDirectoryEntry | ExplorerFileEntry;
}) {
  return (
    <li className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-start gap-x-2.5 overflow-hidden px-3 py-2">
      <span
        aria-hidden="true"
        className={cn(
          "mt-0.5 inline-flex size-4 shrink-0 items-center justify-center [&_svg]:size-4",
          entry.type === "directory"
            ? "text-amber-500"
            : "text-muted-foreground",
        )}
      >
        {entry.type === "directory" ? <FolderIcon /> : <FileTextIcon />}
      </span>
      <div className="min-w-0 overflow-hidden">
        <div className="min-w-0 max-w-full overflow-hidden break-all text-sm font-medium text-foreground [display:-webkit-box] [-webkit-box-orient:vertical] [-webkit-line-clamp:3]">
          {entry.name}
        </div>
        <div className="mt-0.5 block min-w-0 max-w-full truncate text-[11px] text-muted-foreground">
          {entry.path}
        </div>
      </div>
    </li>
  );
}

function isBucketNotFoundError(error: unknown) {
  return (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    error.code === "bucket_not_found"
  );
}

function isObjectExistsError(error: unknown) {
  return (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    error.code === "object_exists"
  );
}
