import { useId, useState } from "react";
import {
  ArrowUpRightIcon,
  HardDriveIcon,
  LoaderCircleIcon,
  PinIcon,
  ShieldAlertIcon,
  Trash2Icon,
} from "lucide-react";
import { Link } from "react-router-dom";
import type { Bucket, Site } from "@/api/types";
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
import { Input } from "@/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { formatDate } from "@/lib/format";
import { useI18n } from "@/lib/i18n";

export function BucketCard({
  bucket,
  pinned,
  onTogglePin,
  onDelete,
  deleteDisabled,
  deletePending,
  sites,
}: {
  bucket: Bucket;
  pinned: boolean;
  onTogglePin: (id: number) => void;
  onDelete: (name: string) => Promise<void>;
  deleteDisabled: boolean;
  deletePending: boolean;
  sites: Site[];
}) {
  const { locale, t } = useI18n();
  return (
    <Card className="relative min-h-0 min-w-0 overflow-hidden border-border/70 bg-card">
      <HardDriveIcon
        aria-hidden="true"
        className="pointer-events-none absolute top-1/2 right-2 size-36 -translate-y-2/3 text-muted-foreground/10"
      />
      <CardHeader className="flex shrink-0 flex-col gap-3">
        <div className="flex w-full min-w-0 items-start gap-2">
          <CardTitle className="min-w-0 flex-1 text-2xl leading-tight break-all">
            <Link
              className="block truncate hover:underline"
              to={`/buckets/${bucket.name}`}
            >
              {bucket.name}
            </Link>
          </CardTitle>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                aria-label={t(
                  pinned ? "buckets.pin.removeLabel" : "buckets.pin.addLabel",
                  { bucket: bucket.name },
                )}
                aria-pressed={pinned}
                onClick={() => onTogglePin(bucket.id)}
                size="icon-sm"
                type="button"
                variant={pinned ? "secondary" : "ghost"}
              >
                <PinIcon
                  aria-hidden="true"
                  fill={pinned ? "currentColor" : "none"}
                />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              {t(pinned ? "buckets.pin.remove" : "buckets.pin.add")}
            </TooltipContent>
          </Tooltip>
        </div>
        <CardDescription>{t("buckets.list.openHint")}</CardDescription>
      </CardHeader>
      <CardContent className="flex shrink-0 flex-col gap-3">
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
      <CardFooter className="mt-auto shrink-0 justify-end gap-2">
        <DeleteBucketButton
          bucket={bucket}
          disabled={deleteDisabled}
          onDelete={onDelete}
          pending={deletePending}
          sites={sites}
        />
        <Button asChild size="sm" variant="outline">
          <Link to={`/buckets/${bucket.name}`}>
            <ArrowUpRightIcon />
            {t("common.open")}
          </Link>
        </Button>
      </CardFooter>
    </Card>
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
        <Button
          disabled={disabled}
          size="sm"
          type="button"
          variant="destructive"
        >
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
