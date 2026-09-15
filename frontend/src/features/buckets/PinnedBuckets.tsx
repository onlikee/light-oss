import { InfoIcon } from "lucide-react";
import type { Bucket, Site } from "@/api/types";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { useI18n } from "@/lib/i18n";
import { BucketCard } from "./BucketCard";

export function PinnedBuckets({
  buckets,
  onTogglePin,
  onDeleteBucket,
  deleteDisabled,
  deletePendingBucket,
  sitesByBucket,
}: {
  buckets: Bucket[];
  onTogglePin: (id: number) => void;
  onDeleteBucket: (name: string) => Promise<void>;
  deleteDisabled: boolean;
  deletePendingBucket: string;
  sitesByBucket: Record<string, Site[]>;
}) {
  const { t } = useI18n();
  if (buckets.length === 0) return null;

  return (
    <section
      aria-label={t("buckets.pin.title")}
      className="flex flex-col gap-4"
    >
      <div className="flex items-center gap-2">
        <h2 className="text-xl font-semibold tracking-tight">
          {t("buckets.pin.title")}
        </h2>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              aria-label={t("buckets.pin.localHint")}
              size="icon-sm"
              type="button"
              variant="ghost"
            >
              <InfoIcon aria-hidden="true" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t("buckets.pin.localHint")}</TooltipContent>
        </Tooltip>
      </div>
      <div className="grid auto-rows-[18rem] gap-4 md:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4 min-[1800px]:grid-cols-5">
        {buckets.map((bucket) => (
          <BucketCard
            key={bucket.id}
            bucket={bucket}
            pinned
            onTogglePin={onTogglePin}
            onDelete={onDeleteBucket}
            deleteDisabled={deleteDisabled || deletePendingBucket !== ""}
            deletePending={deletePendingBucket === bucket.name}
            sites={sitesByBucket[bucket.name] ?? []}
          />
        ))}
      </div>
    </section>
  );
}
