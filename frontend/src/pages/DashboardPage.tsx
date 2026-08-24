import { useQuery } from "@tanstack/react-query";
import {
  CircleAlertIcon,
  Clock3Icon,
  CpuIcon,
  HardDriveIcon,
  MemoryStickIcon,
  ServerIcon,
  ShieldCheckIcon,
} from "lucide-react";
import { listBuckets } from "@/api/buckets";
import { getSystemStats } from "@/api/system";
import type { StorageLimitStatus, SystemStorageStats } from "@/api/types";
import { StatCard } from "@/components/StatCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { getApiHostLabel, hasBearerToken } from "@/lib/connection";
import { formatBytes, formatDate, formatPercent } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import { useAppSettings } from "@/lib/settings";

export function DashboardPage() {
  const { settings } = useAppSettings();
  const { locale, t } = useI18n();

  const tokenConfigured = hasBearerToken(settings.bearerToken);
  const systemStatsEnabled =
    settings.apiBaseUrl.trim() !== "" && tokenConfigured;

  const bucketsQuery = useQuery({
    queryKey: ["buckets", settings.apiBaseUrl, settings.bearerToken],
    queryFn: () => listBuckets(settings),
    enabled: settings.apiBaseUrl.trim() !== "",
  });

  const systemStatsQuery = useQuery({
    queryKey: ["system-stats", settings.apiBaseUrl, settings.bearerToken],
    queryFn: () => getSystemStats(settings),
    enabled: systemStatsEnabled,
    refetchInterval: 10000,
    retry: false,
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
  });

  const buckets = bucketsQuery.data?.items ?? [];
  const latestBucket = buckets.reduce<(typeof buckets)[number] | null>(
    (latest, current) => {
      if (!latest) {
        return current;
      }

      return new Date(current.updated_at).getTime() >
        new Date(latest.updated_at).getTime()
        ? current
        : latest;
    },
    null,
  );

  const host = getApiHostLabel(settings.apiBaseUrl);
  const systemStats = systemStatsQuery.data;
  const storageStats = systemStats?.storage;
  const disks = systemStats?.disks ?? [];
  const systemFallbackText = getSystemFallbackText(t, {
    tokenConfigured,
    enabled: systemStatsEnabled,
    isPending: systemStatsQuery.isPending,
    isError: systemStatsQuery.isError,
  });

  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
        <div className="flex flex-col gap-2">
          <h1 className="text-3xl font-semibold tracking-tight">
            {t("dashboard.title")}
          </h1>
          <p className="max-w-3xl text-sm text-muted-foreground">
            {t("dashboard.description")}
          </p>
        </div>

        <div className="flex flex-wrap gap-2">
          <Badge
            className="h-6 gap-2 px-2.5 text-sm [&>svg]:size-4!"
            variant="secondary"
          >
            <ServerIcon className="size-4" />
            {host}
          </Badge>
          <Badge
            className="h-6 gap-2 px-2.5 text-sm [&>svg]:size-4!"
            variant={tokenConfigured ? "secondary" : "outline"}
          >
            <ShieldCheckIcon className="size-4" />
            {tokenConfigured
              ? t("header.authConfigured")
              : t("header.authMissing")}
          </Badge>
        </div>
      </div>

      {bucketsQuery.isError ? (
        <Alert variant="destructive">
          <CircleAlertIcon />
          <AlertTitle>{t("errors.loadBuckets")}</AlertTitle>
          <AlertDescription>{bucketsQuery.error.message}</AlertDescription>
        </Alert>
      ) : null}

      {systemStatsQuery.isError ? (
        <Alert variant="destructive">
          <CircleAlertIcon />
          <AlertTitle>{t("errors.loadSystemStats")}</AlertTitle>
          <AlertDescription>{systemStatsQuery.error.message}</AlertDescription>
        </Alert>
      ) : null}

      <div className="grid gap-4 sm:grid-cols-2 2xl:grid-cols-4">
        <StatCard
          description={t("buckets.list.total", { count: buckets.length })}
          icon={HardDriveIcon}
          title={t("buckets.overview.totalBuckets")}
          value={String(buckets.length)}
        />
        <StatCard
          description={settings.apiBaseUrl.trim() || t("common.notAvailable")}
          icon={ServerIcon}
          title={t("buckets.overview.apiHost")}
          value={host}
        />
        <StatCard
          description={t("header.connection")}
          icon={ShieldCheckIcon}
          title={t("buckets.overview.authStatus")}
          value={tokenConfigured ? t("common.configured") : t("common.missing")}
        />
        <StatCard
          description={
            latestBucket
              ? latestBucket.name
              : t("buckets.overview.emptyTimestamp")
          }
          icon={Clock3Icon}
          title={t("buckets.overview.latestBucket")}
          value={
            latestBucket
              ? formatDate(latestBucket.updated_at, locale)
              : t("common.noData")
          }
        />
      </div>

      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-2">
          <h2 className="text-xl font-semibold tracking-tight">
            {t("dashboard.system.title")}
          </h2>
          <p className="text-sm text-muted-foreground">
            {t("dashboard.system.description")}
          </p>
        </div>

        <Separator />

        <div className="grid gap-4 sm:grid-cols-2 2xl:grid-cols-4">
          <StatCard
            description={t("dashboard.system.hostOSDescription", { host })}
            icon={ServerIcon}
            title={t("dashboard.system.hostOS")}
            value={
              systemStats ? getOSLabel(t, systemStats.os) : systemFallbackText
            }
          />
          <StatCard
            description={t("dashboard.system.cpuDescription")}
            icon={CpuIcon}
            title={t("dashboard.system.cpuUsage")}
            value={
              systemStats
                ? formatPercent(systemStats.cpu.used_percent)
                : systemFallbackText
            }
          />
          <StatCard
            description={
              systemStats
                ? t("dashboard.system.memoryDescription", {
                    available: formatBytes(systemStats.memory.available_bytes),
                  })
                : systemFallbackText
            }
            icon={MemoryStickIcon}
            title={t("dashboard.system.memoryUsage")}
            value={
              systemStats
                ? formatPercent(systemStats.memory.used_percent)
                : systemFallbackText
            }
          />
          <StatCard
            description={storageStats?.root_path ?? systemFallbackText}
            icon={HardDriveIcon}
            title={t("dashboard.system.storageUsed")}
            value={
              storageStats
                ? `${formatBytes(storageStats.used_bytes)} / ${formatBytes(storageStats.max_bytes)}`
                : systemFallbackText
            }
          />
        </div>

        {storageStats ? (
          <Card className="border-border/70 bg-card">
            <CardHeader>
              <CardTitle>{t("dashboard.system.storageUsed")}</CardTitle>
              <CardDescription>{storageStats.root_path}</CardDescription>
              <CardAction>
                <Badge
                  variant={getStorageStatusBadgeVariant(
                    storageStats.limit_status,
                  )}
                >
                  {getStorageStatusLabel(t, storageStats.limit_status)}
                </Badge>
              </CardAction>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex min-w-56 flex-col gap-2">
                <div className="flex items-center justify-between gap-3 text-sm text-muted-foreground">
                  <span>
                    {formatBytes(storageStats.used_bytes)} /{" "}
                    {formatBytes(storageStats.max_bytes)}
                  </span>
                  <span>{formatPercent(storageStats.used_percent)}</span>
                </div>
                <Progress value={clampPercent(storageStats.used_percent)} />
              </div>

              {storageStats.limit_status !== "ok" ? (
                <Alert
                  variant={
                    storageStats.limit_status === "exceeded"
                      ? "destructive"
                      : "default"
                  }
                >
                  <CircleAlertIcon />
                  <AlertTitle>
                    {getStorageAlertTitle(t, storageStats.limit_status)}
                  </AlertTitle>
                  <AlertDescription>
                    {getStorageAlertDescription(t, storageStats)}
                  </AlertDescription>
                </Alert>
              ) : null}
            </CardContent>
          </Card>
        ) : null}

        <Card className="border-border/70 bg-card">
          <CardHeader>
            <CardTitle>{t("dashboard.system.disksTitle")}</CardTitle>
            <CardDescription>
              {t("dashboard.system.disksDescription")}
            </CardDescription>
            <CardAction>
              <Badge variant="outline">{disks.length}</Badge>
            </CardAction>
          </CardHeader>
          <CardContent>
            {systemStatsEnabled && systemStatsQuery.isPending ? (
              <DiskTableSkeleton />
            ) : disks.length > 0 ? (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("dashboard.system.diskLabel")}</TableHead>
                    <TableHead>{t("dashboard.system.mountPoint")}</TableHead>
                    <TableHead>{t("dashboard.system.filesystem")}</TableHead>
                    <TableHead className="w-full">
                      {t("dashboard.system.usage")}
                    </TableHead>
                    <TableHead>{t("dashboard.system.storageRoot")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {disks.map((disk) => (
                    <TableRow key={`${disk.mount_point}:${disk.label}`}>
                      <TableCell className="font-medium">
                        {disk.label}
                      </TableCell>
                      <TableCell>{disk.mount_point}</TableCell>
                      <TableCell>{disk.filesystem || "-"}</TableCell>
                      <TableCell className="w-full whitespace-normal">
                        <div className="flex min-w-56 flex-col gap-2">
                          <div className="flex items-center justify-between gap-3 text-sm text-muted-foreground">
                            <span>
                              {formatBytes(disk.used_bytes)} /{" "}
                              {formatBytes(disk.total_bytes)}
                            </span>
                            <span>{formatPercent(disk.used_percent)}</span>
                          </div>
                          <Progress value={clampPercent(disk.used_percent)} />
                        </div>
                      </TableCell>
                      <TableCell>
                        {disk.contains_storage_root ? (
                          <Badge variant="secondary">
                            {t("dashboard.system.storageRootBadge")}
                          </Badge>
                        ) : (
                          <span className="text-muted-foreground">-</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : (
              <p className="text-sm text-muted-foreground">
                {systemStatsEnabled
                  ? t("dashboard.system.disksEmpty")
                  : systemFallbackText}
              </p>
            )}
          </CardContent>
        </Card>
      </div>

    </section>
  );
}

function DiskTableSkeleton() {
  return (
    <div className="flex flex-col gap-3">
      {Array.from({ length: 3 }).map((_, index) => (
        <div
          className="grid gap-3 md:grid-cols-[1.2fr_1.5fr_1fr_2fr_1fr]"
          key={index}
        >
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
        </div>
      ))}
    </div>
  );
}

function getSystemFallbackText(
  t: ReturnType<typeof useI18n>["t"],
  {
    tokenConfigured,
    enabled,
    isPending,
    isError,
  }: {
    tokenConfigured: boolean;
    enabled: boolean;
    isPending: boolean;
    isError: boolean;
  },
) {
  if (!enabled) {
    return tokenConfigured ? t("common.notAvailable") : t("common.missing");
  }
  if (isPending) {
    return t("common.loading");
  }
  if (isError) {
    return t("common.notAvailable");
  }

  return t("common.noData");
}

function getOSLabel(
  t: ReturnType<typeof useI18n>["t"],
  value: "windows" | "linux" | "macos" | "other",
) {
  switch (value) {
    case "windows":
      return t("dashboard.system.os.windows");
    case "linux":
      return t("dashboard.system.os.linux");
    case "macos":
      return t("dashboard.system.os.macos");
    default:
      return t("dashboard.system.os.other");
  }
}

function clampPercent(value: number) {
  return Math.max(0, Math.min(100, value));
}

function getStorageStatusLabel(
  t: ReturnType<typeof useI18n>["t"],
  status: StorageLimitStatus,
) {
  switch (status) {
    case "warning":
      return t("settings.storage.status.warning");
    case "exceeded":
      return t("settings.storage.status.exceeded");
    default:
      return t("settings.storage.status.ok");
  }
}

function getStorageStatusBadgeVariant(status: StorageLimitStatus) {
  switch (status) {
    case "warning":
      return "secondary" as const;
    case "exceeded":
      return "destructive" as const;
    default:
      return "outline" as const;
  }
}

function getStorageAlertTitle(
  t: ReturnType<typeof useI18n>["t"],
  status: StorageLimitStatus,
) {
  switch (status) {
    case "warning":
      return t("dashboard.system.storageAlertWarningTitle");
    case "exceeded":
      return t("dashboard.system.storageAlertExceededTitle");
    default:
      return "";
  }
}

function getStorageAlertDescription(
  t: ReturnType<typeof useI18n>["t"],
  storageStats: SystemStorageStats,
) {
  switch (storageStats.limit_status) {
    case "warning":
      return t("dashboard.system.storageAlertWarningDescription", {
        used: formatBytes(storageStats.used_bytes),
        max: formatBytes(storageStats.max_bytes),
      });
    case "exceeded":
      return t("dashboard.system.storageAlertExceededDescription", {
        used: formatBytes(storageStats.used_bytes),
        max: formatBytes(storageStats.max_bytes),
      });
    default:
      return "";
  }
}
