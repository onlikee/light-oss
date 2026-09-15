import { type FormEvent } from "react";
import { LoaderCircleIcon, RefreshCcwIcon, SearchIcon } from "lucide-react";
import type { Bucket, Site } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { CreateBucketDialog } from "@/features/buckets/CreateBucketForm";
import { BucketCard } from "./BucketCard";
import { useI18n } from "@/lib/i18n";

export function BucketList({
  buckets,
  createPending,
  deleteDisabled,
  deletePendingBucket,
  loading = false,
  pinnedIds,
  onTogglePin,
  onCreateBucket,
  onDeleteBucket,
  onRefreshBuckets,
  onSearchInputChange,
  onSearchSubmit,
  refreshPending = false,
  search,
  searchInput,
  sitesByBucket,
}: {
  buckets: Bucket[];
  createPending: boolean;
  deleteDisabled: boolean;
  deletePendingBucket: string;
  loading?: boolean;
  pinnedIds: number[];
  onTogglePin: (id: number) => void;
  onCreateBucket: (name: string) => Promise<void>;
  onDeleteBucket: (bucketName: string) => Promise<void>;
  onRefreshBuckets: () => Promise<void>;
  onSearchInputChange: (value: string) => void;
  onSearchSubmit: (event: FormEvent<HTMLFormElement>) => void;
  refreshPending?: boolean;
  search: string;
  searchInput: string;
  sitesByBucket: Record<string, Site[]>;
}) {
  const { t } = useI18n();
  const hasActiveSearch = search !== "";

  return (
    <section
      aria-label={t("buckets.list.title")}
      className="flex flex-col gap-4"
    >
      <div className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-xl font-semibold tracking-tight">
              {t("buckets.list.title")}
            </h2>
            <Badge variant="secondary">
              {t("buckets.list.total", { count: buckets.length })}
            </Badge>
          </div>
        </div>

        <div className="flex w-full min-w-0 items-center gap-2 lg:max-w-xl lg:justify-end">
          <form
            className="flex min-w-0 flex-1 items-center gap-2 lg:max-w-sm"
            onSubmit={onSearchSubmit}
          >
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  aria-label={t("common.refresh")}
                  className="shrink-0"
                  disabled={refreshPending}
                  onClick={() => void onRefreshBuckets()}
                  size="icon"
                  type="button"
                  variant="outline"
                >
                  {refreshPending ? (
                    <LoaderCircleIcon className="animate-spin" />
                  ) : (
                    <RefreshCcwIcon />
                  )}
                </Button>
              </TooltipTrigger>
              <TooltipContent
                className="whitespace-nowrap leading-none"
                sideOffset={6}
              >
                {t("common.refresh")}
              </TooltipContent>
            </Tooltip>
            <FieldGroup className="min-w-0 flex-1">
              <Field className="min-w-0" orientation="responsive">
                <FieldLabel className="sr-only" htmlFor="bucket-search">
                  {t("buckets.search.label")}
                </FieldLabel>
                <Input
                  className="min-w-0"
                  id="bucket-search"
                  onChange={(event) => onSearchInputChange(event.target.value)}
                  placeholder={t("buckets.search.placeholder")}
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

      <div className="grid auto-rows-[18rem] gap-4 md:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4 min-[1800px]:grid-cols-5">
        <CreateBucketDialog onSubmit={onCreateBucket} pending={createPending} />
        {buckets.map((bucket) => (
          <BucketCard
            key={bucket.id}
            bucket={bucket}
            pinned={pinnedIds.includes(bucket.id)}
            onTogglePin={onTogglePin}
            onDelete={onDeleteBucket}
            deleteDisabled={deleteDisabled || deletePendingBucket !== ""}
            deletePending={deletePendingBucket === bucket.name}
            sites={sitesByBucket[bucket.name] ?? []}
          />
        ))}
      </div>

      {!loading && buckets.length === 0 && hasActiveSearch ? (
        <Empty className="min-h-48 border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <SearchIcon />
            </EmptyMedia>
            <EmptyTitle>{t("buckets.search.emptyTitle")}</EmptyTitle>
            <EmptyDescription>
              {t("buckets.search.emptyDescription")}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : null}
    </section>
  );
}
