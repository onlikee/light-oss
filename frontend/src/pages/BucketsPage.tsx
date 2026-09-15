import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleAlertIcon } from "lucide-react";
import { FormEvent, useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import type { Bucket, Site } from "@/api/types";
import { createBucket, listBuckets } from "@/api/buckets";
import { listSites } from "@/api/sites";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { BucketList } from "@/features/buckets/BucketList";
import { PinnedBuckets } from "@/features/buckets/PinnedBuckets";
import { useDeleteBucket } from "@/features/buckets/useDeleteBucket";
import { usePinnedBuckets } from "@/features/buckets/usePinnedBuckets";
import { useI18n } from "@/lib/i18n";
import { useAppSettings } from "@/lib/settings";

export function BucketsPage() {
  const { settings } = useAppSettings();
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const { pinnedIds, togglePin, removePin, reconcilePins } = usePinnedBuckets(
    settings.apiBaseUrl,
  );
  const reconciledSnapshot = useRef("");
  const [searchParams, setSearchParams] = useSearchParams();
  const { deleteBucket, deletingBucketName } = useDeleteBucket(removePin);
  const [searchInput, setSearchInput] = useState("");
  const search = normalizeBucketSearch(searchParams.get("search"));
  const bucketsBaseQueryKey = [
    "buckets",
    settings.apiBaseUrl,
    settings.bearerToken,
  ] as const;
  const bucketsQueryKey = [...bucketsBaseQueryKey, search] as const;
  const sitesQueryKey = [
    "sites",
    settings.apiBaseUrl,
    settings.bearerToken,
  ] as const;

  useEffect(() => {
    setSearchInput(search);
  }, [search]);

  const bucketsQuery = useQuery({
    queryKey: bucketsQueryKey,
    queryFn: () => listBuckets(settings, { search }),
    enabled: settings.apiBaseUrl.trim() !== "",
  });

  const pinnedBucketsQuery = useQuery({
    queryKey: [...bucketsBaseQueryKey, ""],
    queryFn: () => listBuckets(settings, { search: "" }),
    enabled:
      settings.apiBaseUrl.trim() !== "" &&
      search !== "" &&
      pinnedIds.length > 0,
  });
  const fullBucketsQuery = search === "" ? bucketsQuery : pinnedBucketsQuery;
  const fullBuckets = fullBucketsQuery.data?.items;
  const fullSnapshot = JSON.stringify([
    ...bucketsBaseQueryKey,
    fullBucketsQuery.dataUpdatedAt,
  ]);

  useEffect(() => {
    if (
      fullBucketsQuery.isSuccess &&
      fullBucketsQuery.isFetchedAfterMount &&
      !fullBucketsQuery.isFetching &&
      fullBuckets &&
      reconciledSnapshot.current !== fullSnapshot
    ) {
      reconciledSnapshot.current = fullSnapshot;
      reconcilePins(fullBuckets.map((bucket) => bucket.id));
    }
  }, [
    fullBuckets,
    fullSnapshot,
    fullBucketsQuery.isSuccess,
    fullBucketsQuery.isFetchedAfterMount,
    fullBucketsQuery.isFetching,
    reconcilePins,
  ]);

  const sitesQuery = useQuery({
    queryKey: sitesQueryKey,
    queryFn: () => listSites(settings),
    enabled: settings.apiBaseUrl.trim() !== "",
  });

  const createBucketMutation = useMutation({
    mutationFn: (name: string) => createBucket(settings, name),
    onSuccess: async () => {
      toast.success(t("toast.bucketCreated"));
      await queryClient.invalidateQueries({ queryKey: bucketsBaseQueryKey });
    },
    onError: (error) => {
      const message =
        error instanceof Error ? error.message : t("errors.createBucket");
      toast.error(message);
    },
  });

  async function handleCreateBucket(name: string) {
    await createBucketMutation.mutateAsync(name);
  }

  async function handleDeleteBucket(bucketName: string) {
    const bucket =
      fullBuckets?.find((item) => item.name === bucketName) ??
      bucketsQuery.data?.items.find((item) => item.name === bucketName);
    await deleteBucket({ name: bucketName, id: bucket?.id });
  }

  function updateSearchParams(nextSearch: string) {
    const next = new URLSearchParams(searchParams);

    if (!nextSearch) {
      next.delete("search");
    } else {
      next.set("search", nextSearch);
    }

    setSearchParams(next, { replace: false });
  }

  function handleSearchSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    updateSearchParams(searchInput.trim());
  }

  async function handleRefreshBuckets() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: bucketsBaseQueryKey }),
      queryClient.invalidateQueries({ queryKey: sitesQueryKey }),
    ]);
  }

  const buckets = bucketsQuery.data?.items ?? [];
  const bucketsById = new Map(
    fullBuckets?.map((bucket) => [bucket.id, bucket]),
  );
  const pinnedBuckets = pinnedIds
    .map((id) => bucketsById.get(id))
    .filter((bucket): bucket is Bucket => bucket !== undefined);
  const sites = sitesQuery.data?.items ?? [];
  const sitesByBucket: Record<string, Site[]> = {};

  for (const site of sites) {
    if (!sitesByBucket[site.bucket]) {
      sitesByBucket[site.bucket] = [];
    }
    sitesByBucket[site.bucket].push(site);
  }

  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-2">
          <h1 className="text-3xl font-semibold tracking-tight">
            {t("buckets.title")}
          </h1>
          <p className="max-w-3xl text-sm text-muted-foreground">
            {t("buckets.description")}
          </p>
        </div>
      </div>

      {bucketsQuery.isError ? (
        <Alert variant="destructive">
          <CircleAlertIcon />
          <AlertTitle>{t("errors.loadBuckets")}</AlertTitle>
          <AlertDescription>{bucketsQuery.error.message}</AlertDescription>
        </Alert>
      ) : null}

      {sitesQuery.isError ? (
        <Alert variant="destructive">
          <CircleAlertIcon />
          <AlertTitle>{t("errors.loadSites")}</AlertTitle>
          <AlertDescription>{sitesQuery.error.message}</AlertDescription>
        </Alert>
      ) : null}

      {search !== "" && pinnedIds.length > 0 && pinnedBucketsQuery.isError ? (
        <Alert variant="destructive">
          <CircleAlertIcon />
          <AlertTitle>{t("buckets.pin.loadError")}</AlertTitle>
          <AlertDescription>
            {pinnedBucketsQuery.error.message}
          </AlertDescription>
        </Alert>
      ) : null}

      <PinnedBuckets
        buckets={pinnedBuckets}
        onTogglePin={togglePin}
        onDeleteBucket={handleDeleteBucket}
        deleteDisabled={sitesQuery.isLoading || sitesQuery.isError}
        deletePendingBucket={deletingBucketName}
        sitesByBucket={sitesByBucket}
      />

      <BucketList
        pinnedIds={pinnedIds}
        onTogglePin={togglePin}
        buckets={buckets}
        createPending={createBucketMutation.isPending}
        deleteDisabled={sitesQuery.isLoading || sitesQuery.isError}
        deletePendingBucket={deletingBucketName}
        loading={bucketsQuery.isLoading}
        onCreateBucket={handleCreateBucket}
        onDeleteBucket={handleDeleteBucket}
        onRefreshBuckets={handleRefreshBuckets}
        onSearchInputChange={setSearchInput}
        onSearchSubmit={handleSearchSubmit}
        refreshPending={
          bucketsQuery.isFetching ||
          pinnedBucketsQuery.isFetching ||
          sitesQuery.isFetching
        }
        search={search}
        searchInput={searchInput}
        sitesByBucket={sitesByBucket}
      />
    </section>
  );
}

function normalizeBucketSearch(value: string | null | undefined) {
  return (value ?? "").trim();
}
