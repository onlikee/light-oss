import { type FormEvent, useId, useState } from "react";
import {
  ArrowUpRightIcon,
  HardDriveIcon,
  LoaderCircleIcon,
  RefreshCcwIcon,
  SearchIcon,
  ShieldAlertIcon,
  Trash2Icon,
} from "lucide-react";
import { Link } from "react-router-dom";
import type { Bucket, Site } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
import { formatDate } from "@/lib/format";
import { useI18n } from "@/lib/i18n";

export function BucketList({
  buckets,
  createPending,
  deleteDisabled,
  deletePendingBucket,
  loading = false,
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
  const { locale, t } = useI18n();
  const hasActiveSearch = search !== "";

  return (
    <div className="flex flex-col gap-4">
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

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4 min-[1800px]:grid-cols-5">
        <CreateBucketDialog onSubmit={onCreateBucket} pending={createPending} />
        {buckets.map((bucket) => (
          <Card
            className="relative overflow-hidden border-border/70 bg-card"
            key={bucket.id}
          >
            <HardDriveIcon
              aria-hidden="true"
              className="pointer-events-none absolute top-1/2 right-2 size-36 -translate-y-2/3 text-muted-foreground/10"
            />
            <CardHeader className="flex flex-col gap-3">
              <CardTitle className="text-2xl leading-tight break-all">
                <Link
                  className="hover:underline"
                  to={`/buckets/${bucket.name}`}
                >
                  {bucket.name}
                </Link>
              </CardTitle>
              <CardDescription>{t("buckets.list.openHint")}</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <div className="flex flex-col gap-1">
                <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  {t("buckets.table.createdAt")}
                </span>
                <span className="text-sm">
                  {formatDate(bucket.created_at, locale)}
                </span>
              </div>
              <div className="flex flex-col gap-1">
                <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  {t("buckets.table.updatedAt")}
                </span>
                <span className="text-sm">
                  {formatDate(bucket.updated_at, locale)}
                </span>
              </div>
            </CardContent>
            <CardFooter className="justify-end gap-2">
              <DeleteBucketButton
                bucket={bucket}
                disabled={deleteDisabled || deletePendingBucket !== ""}
                onDelete={onDeleteBucket}
                pending={deletePendingBucket === bucket.name}
                sites={sitesByBucket[bucket.name] ?? []}
              />
              <Button asChild size="sm" variant="outline">
                <Link to={`/buckets/${bucket.name}`}>
                  <ArrowUpRightIcon />
                  {t("common.open")}
                </Link>
              </Button>
            </CardFooter>
          </Card>
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
    </div>
  );
}

function DeleteBucketButton({
  bucket,
  disabled,
  onDelete,
  pending,
  sites,
}: {
  bucket: Bucket;
  disabled: boolean;
  onDelete: (bucketName: string) => Promise<void>;
  pending: boolean;
  sites: Site[];
}) {
  const [confirmationValue, setConfirmationValue] = useState("");
  const [open, setOpen] = useState(false);
  const { t } = useI18n();
  const inputId = useId();
  const isMatch = confirmationValue.trim() === bucket.name;

  function handleOpenChange(nextOpen: boolean) {
    setOpen(nextOpen);
    setConfirmationValue("");
  }

  async function handleDelete() {
    if (!isMatch) {
      return;
    }

    try {
      await onDelete(bucket.name);
      setOpen(false);
    } catch {
      // Mutation error state is handled by the page-level toast.
    }
  }

  return (
    <AlertDialog onOpenChange={handleOpenChange} open={open}>
      <AlertDialogTrigger asChild>
        <Button disabled={disabled} size="sm" type="button" variant="destructive">
          <Trash2Icon />
          {t("common.delete")}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent className="sm:max-w-md">
        <AlertDialogHeader>
          <AlertDialogMedia>
            <ShieldAlertIcon />
          </AlertDialogMedia>
          <AlertDialogTitle>{t("buckets.delete.title")}</AlertDialogTitle>
          <AlertDialogDescription>
            {t("buckets.delete.description", { bucket: bucket.name })}
          </AlertDialogDescription>
        </AlertDialogHeader>

        {sites.length > 0 ? (
          <div className="grid gap-2">
            <div className="text-sm font-medium">
              {t("buckets.delete.sitesTitle")}
            </div>
            <div className="max-h-40 overflow-y-auto rounded-lg border border-border/70 bg-muted/30 p-3">
              <div className="grid gap-3">
                {sites.map((site) => (
                  <div className="grid gap-1 text-sm" key={site.id}>
                    <div>
                      {t("sites.table.rootPrefix")}:{" "}
                      <span className="wrap-anywhere">
                        {site.root_prefix || t("explorer.rootFolder")}
                      </span>
                    </div>
                    <div>
                      {t("sites.table.domains")}:{" "}
                      <span className="wrap-anywhere">
                        {site.domains.join(", ") || t("common.noData")}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>
        ) : null}

        <div className="grid gap-2">
          <label className="text-sm font-medium" htmlFor={inputId}>
            {t("buckets.delete.confirmLabel")}
          </label>
          <Input
            autoComplete="off"
            id={inputId}
            onChange={(event) => setConfirmationValue(event.target.value)}
            placeholder={t("buckets.delete.confirmPlaceholder")}
            value={confirmationValue}
          />
          <p className="text-sm text-muted-foreground">
            {t("buckets.delete.confirmDescription", { bucket: bucket.name })}
          </p>
        </div>

        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <Button
            disabled={!isMatch || pending}
            onClick={() => void handleDelete()}
            type="button"
            variant="destructive"
          >
            {pending ? (
              <LoaderCircleIcon
                className="animate-spin"
                data-icon="inline-start"
              />
            ) : null}
            {pending
              ? t("buckets.delete.submitting")
              : t("buckets.delete.submit")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
