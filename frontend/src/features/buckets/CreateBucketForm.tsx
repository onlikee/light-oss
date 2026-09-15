import { FormEvent, useState } from "react";
import { LoaderCircleIcon, PlusIcon } from "lucide-react";
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
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useI18n } from "@/lib/i18n";

export function CreateBucketDialog({
  onSubmit,
  pending,
}: {
  onSubmit: (name: string) => Promise<void>;
  pending: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const { t } = useI18n();

  function handleOpenChange(nextOpen: boolean) {
    setOpen(nextOpen);

    if (!nextOpen && !pending) {
      setName("");
    }
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!name.trim()) {
      return;
    }

    await onSubmit(name.trim());
    setName("");
    setOpen(false);
  }

  return (
    <Dialog onOpenChange={handleOpenChange} open={open}>
      <DialogTrigger asChild>
        <button
          aria-label={t("buckets.create.title")}
          className="flex min-h-0 cursor-pointer items-center justify-center rounded-xl bg-card text-card-foreground ring-1 ring-foreground/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          type="button"
        >
          <PlusIcon aria-hidden="true" className="size-10 text-muted-foreground" />
        </button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("buckets.create.title")}</DialogTitle>
          <DialogDescription>
            {t("buckets.create.description")}
          </DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-5" onSubmit={handleSubmit}>
          <FieldGroup>
            <Field data-disabled={pending || undefined}>
              <FieldLabel htmlFor="bucket-name">
                {t("buckets.form.name.label")}
              </FieldLabel>
              <Input
                disabled={pending}
                id="bucket-name"
                onChange={(event) => setName(event.target.value)}
                placeholder={t("buckets.form.name.placeholder")}
                value={name}
              />
              <FieldDescription>
                {t("buckets.form.name.description")}
              </FieldDescription>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button disabled={pending || !name.trim()} type="submit">
              {pending ? (
                <LoaderCircleIcon
                  className="animate-spin"
                  data-icon="inline-start"
                />
              ) : (
                <PlusIcon />
              )}
              {pending
                ? t("buckets.form.submitting")
                : t("buckets.form.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
