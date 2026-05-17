import { FormEvent, useEffect, useMemo, useState, type ReactNode } from "react";
import { GlobeIcon, LoaderCircleIcon } from "lucide-react";
import type { Bucket, Site } from "@/api/types";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useI18n } from "@/lib/i18n";
import { siteDomainPlaceholder } from "@/lib/site-domain";

const emptyBuckets: Bucket[] = [];

export interface SiteFormValue {
  bucket: string;
  rootPrefix: string;
  domains: string[];
  enabled: boolean;
  indexDocument: string;
  errorDocument: string;
  spaFallback: boolean;
}

export function buildSiteFormValue(
  value: Partial<SiteFormValue> = {},
): SiteFormValue {
  return {
    bucket: value.bucket ?? "",
    rootPrefix: value.rootPrefix ?? "",
    domains: [...(value.domains ?? [])],
    enabled: value.enabled ?? true,
    indexDocument: value.indexDocument ?? "index.html",
    errorDocument: value.errorDocument ?? "",
    spaFallback: value.spaFallback ?? true,
  };
}

export function siteToSiteFormValue(site: Site): SiteFormValue {
  return buildSiteFormValue({
    bucket: site.bucket,
    rootPrefix: site.root_prefix,
    domains: site.domains,
    enabled: site.enabled,
    indexDocument: site.index_document,
    errorDocument: site.error_document,
    spaFallback: site.spa_fallback,
  });
}

export function SiteFormDialog({
  buckets,
  description,
  initialValue,
  lockedFields,
  mode,
  open,
  pending,
  onOpenChange,
  onSubmit,
  submitLabel,
  title,
  trigger,
  triggerTooltipLabel,
}: {
  buckets?: Bucket[];
  description: string;
  initialValue?: Partial<SiteFormValue>;
  lockedFields?: {
    bucket?: boolean;
    rootPrefix?: boolean;
    indexDocument?: boolean;
  };
  mode: "create" | "edit";
  open?: boolean;
  pending: boolean;
  onOpenChange?: (open: boolean) => void;
  onSubmit: (value: SiteFormValue) => Promise<void>;
  submitLabel?: string;
  title: string;
  trigger?: ReactNode;
  triggerTooltipLabel?: string;
}) {
  const resolvedBuckets = buckets ?? emptyBuckets;
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false);
  const [formValue, setFormValue] = useState<SiteFormValue>(() =>
    buildSiteFormValue(initialValue),
  );
  const [domainsInput, setDomainsInput] = useState("");
  const { t } = useI18n();
  const bucketLocked = lockedFields?.bucket ?? false;
  const rootPrefixLocked = lockedFields?.rootPrefix ?? false;
  const indexDocumentLocked = lockedFields?.indexDocument ?? false;
  const initialValueSnapshot = JSON.stringify(buildSiteFormValue(initialValue));
  const initialDialogValue = useMemo(
    () => JSON.parse(initialValueSnapshot) as SiteFormValue,
    [initialValueSnapshot],
  );
  const defaultBucketName = resolvedBuckets[0]?.name ?? "";
  const dialogOpen = open ?? uncontrolledOpen;
  const handleOpenChange = onOpenChange ?? setUncontrolledOpen;

  useEffect(() => {
    if (!dialogOpen) {
      return;
    }

    const nextValue = buildSiteFormValue(initialDialogValue);
    if (!bucketLocked && nextValue.bucket === "" && defaultBucketName !== "") {
      nextValue.bucket = defaultBucketName;
    }

    setFormValue(nextValue);
    setDomainsInput(nextValue.domains.join(", "));
  }, [bucketLocked, defaultBucketName, dialogOpen, initialDialogValue]);

  const parsedDomains = domainsInput
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean);
  const canSubmit = formValue.bucket.trim() !== "" && parsedDomains.length > 0;
  const currentPrefix = formValue.rootPrefix || t("explorer.rootFolder");
  const resolvedSubmitLabel =
    submitLabel ??
    (mode === "create"
      ? t("sites.form.submitCreate")
      : t("sites.form.submitUpdate"));
  const resolvedTrigger =
    trigger === undefined && open === undefined ? (
      <Button type="button" variant="outline">
        <GlobeIcon />
        {resolvedSubmitLabel}
      </Button>
    ) : (
      trigger
    );

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();

    if (!canSubmit) {
      return;
    }

    try {
      await onSubmit({
        ...formValue,
        bucket: formValue.bucket.trim(),
        rootPrefix: formValue.rootPrefix.trim(),
        domains: parsedDomains,
        indexDocument: formValue.indexDocument.trim() || "index.html",
        errorDocument: formValue.errorDocument.trim(),
      });
      handleOpenChange(false);
    } catch {
      return;
    }
  }

  return (
    <Dialog onOpenChange={handleOpenChange} open={dialogOpen}>
      {resolvedTrigger ? (
        triggerTooltipLabel ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="inline-flex">
                <DialogTrigger asChild>{resolvedTrigger}</DialogTrigger>
              </span>
            </TooltipTrigger>
            <TooltipContent
              className="whitespace-nowrap leading-none"
              sideOffset={6}
            >
              {triggerTooltipLabel}
            </TooltipContent>
          </Tooltip>
        ) : (
          <DialogTrigger asChild>{resolvedTrigger}</DialogTrigger>
        )
      ) : null}
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-5" onSubmit={handleSubmit}>
          <FieldGroup>
            <Field data-disabled={pending || undefined}>
              <FieldLabel htmlFor="site-form-bucket">
                {t("sites.form.bucket")}
              </FieldLabel>
              {bucketLocked ? (
                <div className="rounded-lg border border-border/70 bg-muted px-3 py-2 text-sm text-muted-foreground">
                  {formValue.bucket}
                </div>
              ) : resolvedBuckets.length > 0 ? (
                <Select
                  onValueChange={(value) =>
                    setFormValue((current) => ({ ...current, bucket: value }))
                  }
                  value={formValue.bucket}
                >
                  <SelectTrigger
                    aria-label={t("sites.form.bucket")}
                    className="w-full"
                    disabled={pending}
                    id="site-form-bucket"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent position="popper" side="bottom">
                    <SelectGroup>
                      {resolvedBuckets.map((bucket) => (
                        <SelectItem key={bucket.name} value={bucket.name}>
                          {bucket.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              ) : (
                <div className="rounded-lg border border-border/70 bg-muted px-3 py-2 text-sm text-muted-foreground">
                  {t("common.noData")}
                </div>
              )}
            </Field>

            <Field data-disabled={pending || undefined}>
              <FieldLabel htmlFor="site-form-root-prefix">
                {t("sites.form.rootPrefix")}
              </FieldLabel>
              {rootPrefixLocked ? (
                <div className="rounded-lg border border-border/70 bg-muted px-3 py-2 text-sm text-muted-foreground">
                  {currentPrefix}
                </div>
              ) : (
                <Input
                  disabled={pending}
                  id="site-form-root-prefix"
                  onChange={(event) =>
                    setFormValue((current) => ({
                      ...current,
                      rootPrefix: event.target.value,
                    }))
                  }
                  placeholder="demo/"
                  value={formValue.rootPrefix}
                />
              )}
            </Field>

            <Field data-disabled={pending || undefined}>
              <FieldLabel htmlFor="site-form-domains">
                {t("sites.form.domains")}
              </FieldLabel>
              <Input
                disabled={pending}
                id="site-form-domains"
                onChange={(event) => setDomainsInput(event.target.value)}
                placeholder={siteDomainPlaceholder}
                value={domainsInput}
              />
              <FieldDescription>
                {t("sites.form.domainsDescription")}
              </FieldDescription>
            </Field>

            <Field data-disabled={pending || undefined}>
              <FieldLabel htmlFor="site-form-enabled">
                {t("sites.form.enabled")}
              </FieldLabel>
              <Select
                onValueChange={(value) =>
                  setFormValue((current) => ({
                    ...current,
                    enabled: value === "true",
                  }))
                }
                value={String(formValue.enabled)}
              >
                <SelectTrigger
                  aria-label={t("sites.form.enabled")}
                  className="w-full"
                  disabled={pending}
                  id="site-form-enabled"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent position="popper" side="bottom">
                  <SelectGroup>
                    <SelectItem value="true">{t("common.enabled")}</SelectItem>
                    <SelectItem value="false">{t("common.disabled")}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>

            <Field data-disabled={pending || undefined}>
              <FieldLabel htmlFor="site-form-index-document">
                {t("sites.form.indexDocument")}
              </FieldLabel>
              {indexDocumentLocked ? (
                <div className="rounded-lg border border-border/70 bg-muted px-3 py-2 text-sm text-muted-foreground overflow-x-hidden text-ellipsis whitespace-nowrap">
                  {formValue.indexDocument}
                </div>
              ) : (
                <Input
                  disabled={pending}
                  id="site-form-index-document"
                  onChange={(event) =>
                    setFormValue((current) => ({
                      ...current,
                      indexDocument: event.target.value,
                    }))
                  }
                  value={formValue.indexDocument}
                />
              )}
            </Field>

            <Field data-disabled={pending || undefined}>
              <FieldLabel htmlFor="site-form-error-document">
                {t("sites.form.errorDocument")}
              </FieldLabel>
              <Input
                disabled={pending}
                id="site-form-error-document"
                onChange={(event) =>
                  setFormValue((current) => ({
                    ...current,
                    errorDocument: event.target.value,
                  }))
                }
                placeholder="404.html"
                value={formValue.errorDocument}
              />
            </Field>

            <Field data-disabled={pending || undefined}>
              <FieldLabel htmlFor="site-form-spa-fallback">
                {t("sites.form.spaFallback")}
              </FieldLabel>
              <Select
                onValueChange={(value) =>
                  setFormValue((current) => ({
                    ...current,
                    spaFallback: value === "true",
                  }))
                }
                value={String(formValue.spaFallback)}
              >
                <SelectTrigger
                  aria-label={t("sites.form.spaFallback")}
                  className="w-full"
                  disabled={pending}
                  id="site-form-spa-fallback"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent position="popper" side="bottom">
                  <SelectGroup>
                    <SelectItem value="true">{t("common.enabled")}</SelectItem>
                    <SelectItem value="false">{t("common.disabled")}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
          </FieldGroup>

          <DialogFooter>
            <Button disabled={pending || !canSubmit} type="submit">
              {pending ? (
                <LoaderCircleIcon
                  className="animate-spin"
                  data-icon="inline-start"
                />
              ) : (
                <GlobeIcon />
              )}
              {resolvedSubmitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
