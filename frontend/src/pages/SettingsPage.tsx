import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FormEvent, useEffect, useRef, useState, type ReactNode } from "react";
import { CircleAlertIcon, ShieldAlertIcon } from "lucide-react";
import { toast } from "sonner";
import { getHealthStatus } from "@/api/health";
import { getSystemStats, updateStorageQuota } from "@/api/system";
import type { StorageLimitStatus, SystemStorageStats } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardDescription,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Progress } from "@/components/ui/progress";
import { Separator } from "@/components/ui/separator";
import { ConnectionHealthStatus } from "@/components/ConnectionHealthStatus";
import {
  createCheckingConnectionHealthStates,
  resolveConnectionHealthStates,
  type ConnectionHealthStates,
} from "@/lib/health";
import { hasBearerToken } from "@/lib/connection";
import { formatBytes, formatPercent } from "@/lib/format";
import { LocaleToggle } from "@/components/LocaleToggle";
import { ThemeToggle } from "@/components/ThemeToggle";
import { useI18n } from "@/lib/i18n";
import { useAppSettings } from "@/lib/settings";

export function SettingsPage() {
  const { settings, saveSettings } = useAppSettings();
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [isBearerTokenVisible, setIsBearerTokenVisible] = useState(false);
  const [draftSettings, setDraftSettings] = useState(() => ({
    apiBaseUrl: settings.apiBaseUrl,
    bearerToken: settings.bearerToken,
  }));
  const [storageLimitGiB, setStorageLimitGiB] = useState("");
  const [manualHealthStates, setManualHealthStates] =
    useState<ConnectionHealthStates | null>(null);
  const [isTestingConnection, setIsTestingConnection] = useState(false);
  const testRequestRef = useRef(0);
  const storageQuotaEnabled =
    settings.apiBaseUrl.trim() !== "" && hasBearerToken(settings.bearerToken);
  const systemStatsQueryKey = [
    "system-stats",
    settings.apiBaseUrl,
    settings.bearerToken,
  ] as const;

  const systemStatsQuery = useQuery({
    queryKey: systemStatsQueryKey,
    queryFn: () => getSystemStats(settings),
    enabled: storageQuotaEnabled,
    retry: false,
    refetchOnReconnect: false,
    refetchOnWindowFocus: false,
  });

  const storageQuotaMutation = useMutation({
    mutationFn: (maxBytes: number) => updateStorageQuota(settings, maxBytes),
    onSuccess: async (storageStats) => {
      setStorageLimitGiB(bytesToGiBString(storageStats.max_bytes));
      toast.success(t("toast.storageQuotaSaved"));
      await queryClient.invalidateQueries({ queryKey: systemStatsQueryKey });
    },
    onError: (error) => {
      const message =
        error instanceof Error ? error.message : t("errors.updateStorageQuota");
      toast.error(message);
    },
  });

  const storageStats = systemStatsQuery.data?.storage;

  useEffect(() => {
    setDraftSettings({
      apiBaseUrl: settings.apiBaseUrl,
      bearerToken: settings.bearerToken,
    });
  }, [settings.apiBaseUrl, settings.bearerToken]);

  useEffect(() => {
    if (!storageStats) {
      return;
    }

    setStorageLimitGiB(bytesToGiBString(storageStats.max_bytes));
  }, [storageStats]);

  function readDraftSettings() {
    return {
      apiBaseUrl: draftSettings.apiBaseUrl.trim(),
      bearerToken: draftSettings.bearerToken.trim(),
    };
  }

  function clearManualHealthStates() {
    testRequestRef.current += 1;
    setManualHealthStates(null);
    setIsTestingConnection(false);
  }

  function updateDraftSettings(
    nextDraftSettings: Partial<typeof draftSettings>,
  ) {
    clearManualHealthStates();
    setDraftSettings((current) => ({
      ...current,
      ...nextDraftSettings,
    }));
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const draftSettings = readDraftSettings();
    clearManualHealthStates();
    saveSettings({
      apiBaseUrl: draftSettings.apiBaseUrl,
      bearerToken: draftSettings.bearerToken,
    });
    toast.success(t("toast.settingsSaved"));
  }

  async function handleTestConnection() {
    const draftSettings = readDraftSettings();

    if (draftSettings.apiBaseUrl === "") {
      return;
    }

    const requestId = testRequestRef.current + 1;
    testRequestRef.current = requestId;
    setIsTestingConnection(true);
    setManualHealthStates(createCheckingConnectionHealthStates());

    try {
      const result = await getHealthStatus(draftSettings);
      if (testRequestRef.current !== requestId) {
        return;
      }

      setManualHealthStates(
        resolveConnectionHealthStates({
          isConfigured: true,
          isPending: false,
          error: null,
          data: result,
        }),
      );
    } catch (error) {
      if (testRequestRef.current !== requestId) {
        return;
      }

      setManualHealthStates(
        resolveConnectionHealthStates({
          isConfigured: true,
          isPending: false,
          error,
        }),
      );
    } finally {
      if (testRequestRef.current === requestId) {
        setIsTestingConnection(false);
      }
    }
  }

  function handleSaveStorageLimit() {
    const maxBytes = parseGiBToBytes(storageLimitGiB);
    if (maxBytes === null || maxBytes <= 0) {
      toast.error(t("settings.storage.invalidLimit"));
      return;
    }

    storageQuotaMutation.mutate(maxBytes);
  }

  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold tracking-tight">
          {t("settings.title")}
        </h1>
        <p className="max-w-2xl text-sm text-muted-foreground">
          {t("settings.description")}
        </p>
      </div>

      <div className="space-y-6">
        <Card>
          <CardHeader className="gap-3 border-b">
            <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
              <div className="space-y-1">
                <CardTitle>{t("settings.connection.title")}</CardTitle>
                <CardDescription>
                  {t("settings.connection.description")}
                </CardDescription>
              </div>
              <ConnectionHealthStatus
                className="justify-start lg:justify-end"
                states={manualHealthStates ?? undefined}
              />
            </div>
          </CardHeader>
          <form onSubmit={handleSubmit}>
            <CardContent className="space-y-5 pb-4">
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="api-base-url">
                    {t("settings.connection.apiBaseUrl")}
                  </FieldLabel>
                  <Input
                    id="api-base-url"
                    name="apiBaseUrl"
                    onChange={(event) =>
                      updateDraftSettings({
                        apiBaseUrl: event.currentTarget.value,
                      })
                    }
                    placeholder="http://localhost:8080"
                    value={draftSettings.apiBaseUrl}
                  />
                  <FieldDescription>
                    {t("settings.connection.apiBaseUrlDescription")}
                  </FieldDescription>
                </Field>

                <Field>
                  <FieldLabel htmlFor="bearer-token">
                    {t("settings.connection.bearerToken")}
                  </FieldLabel>
                  <div className="flex items-center gap-2">
                    <div className="flex-1">
                      <Input
                        id="bearer-token"
                        name="bearerToken"
                        onChange={(event) =>
                          updateDraftSettings({
                            bearerToken: event.currentTarget.value,
                          })
                        }
                        placeholder="dev-token"
                        type={isBearerTokenVisible ? "text" : "password"}
                        value={draftSettings.bearerToken}
                      />
                    </div>
                    <Button
                      onClick={() =>
                        setIsBearerTokenVisible((current) => !current)
                      }
                      type="button"
                      variant="outline"
                    >
                      {isBearerTokenVisible
                        ? t("settings.connection.hideToken")
                        : t("settings.connection.showToken")}
                    </Button>
                  </div>
                  <FieldDescription>
                    {t("settings.connection.bearerTokenDescription")}
                  </FieldDescription>
                </Field>
              </FieldGroup>

              <Alert>
                <ShieldAlertIcon />
                <AlertTitle>{t("settings.security.title")}</AlertTitle>
                <AlertDescription>
                  {t("settings.security.description")}
                </AlertDescription>
              </Alert>
            </CardContent>
            <CardFooter className="flex flex-col gap-2 py-3 sm:flex-row sm:justify-end">
              <Button
                className="w-full sm:w-auto"
                disabled={
                  isTestingConnection || readDraftSettings().apiBaseUrl === ""
                }
                onClick={handleTestConnection}
                type="button"
                variant="outline"
              >
                {isTestingConnection
                  ? t("settings.connection.testingConnection")
                  : t("settings.connection.testConnection")}
              </Button>
              <Button className="w-full sm:w-auto" type="submit">
                {t("common.save")}
              </Button>
            </CardFooter>
          </form>
        </Card>

        <Card>
          <CardHeader className="gap-3 border-b">
            <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
              <div className="space-y-1">
                <CardTitle>{t("settings.storage.title")}</CardTitle>
                <CardDescription>
                  {t("settings.storage.description")}
                </CardDescription>
              </div>
              {storageStats ? (
                <Badge
                  className="self-start lg:self-center"
                  variant={getStorageStatusBadgeVariant(
                    storageStats.limit_status,
                  )}
                >
                  {getStorageStatusLabel(t, storageStats.limit_status)}
                </Badge>
              ) : null}
            </div>
          </CardHeader>
          <CardContent className="space-y-5 pb-4 pt-5">
            {systemStatsQuery.isError ? (
              <Alert variant="destructive">
                <CircleAlertIcon />
                <AlertTitle>{t("errors.loadSystemStats")}</AlertTitle>
                <AlertDescription>
                  {systemStatsQuery.error.message}
                </AlertDescription>
              </Alert>
            ) : null}

            {!storageQuotaEnabled ? (
              <p className="text-sm text-muted-foreground">
                {t("settings.storage.unavailable")}
              </p>
            ) : systemStatsQuery.isPending ? (
              <p className="text-sm text-muted-foreground">
                {t("common.loading")}
              </p>
            ) : storageStats ? (
              <>
                <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
                  <StorageMetric
                    label={t("settings.storage.currentUsage")}
                    value={formatBytes(storageStats.used_bytes)}
                  />
                  <StorageMetric
                    label={t("settings.storage.currentLimit")}
                    value={formatBytes(storageStats.max_bytes)}
                  />
                  <StorageMetric
                    label={t("settings.storage.remaining")}
                    value={formatBytes(storageStats.remaining_bytes)}
                  />
                  <StorageMetric
                    label={t("settings.storage.usagePercent")}
                    value={formatPercent(storageStats.used_percent)}
                  />
                  <StorageMetric
                    label={t("settings.storage.status")}
                    value={getStorageStatusLabel(t, storageStats.limit_status)}
                  />
                  <StorageMetric
                    label={t("settings.storage.rootPath")}
                    value={storageStats.root_path}
                  />
                </div>

                <div className="space-y-2">
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

                <FieldGroup>
                  <Field>
                    <FieldLabel htmlFor="storage-limit-gib">
                      {t("settings.storage.limitGiB")}
                    </FieldLabel>
                    <Input
                      id="storage-limit-gib"
                      inputMode="decimal"
                      min="0.1"
                      name="storageLimitGiB"
                      onChange={(event) =>
                        setStorageLimitGiB(event.currentTarget.value)
                      }
                      step="0.1"
                      type="number"
                      value={storageLimitGiB}
                    />
                    <FieldDescription>
                      {t("settings.storage.limitGiBDescription")}
                    </FieldDescription>
                  </Field>
                </FieldGroup>
              </>
            ) : (
              <p className="text-sm text-muted-foreground">
                {t("common.noData")}
              </p>
            )}
          </CardContent>
          <CardFooter className="flex justify-end py-3">
            <Button
              disabled={
                !storageStats ||
                !storageQuotaEnabled ||
                storageQuotaMutation.isPending
              }
              onClick={() => void handleSaveStorageLimit()}
              type="button"
            >
              {storageQuotaMutation.isPending
                ? t("settings.storage.savingLimit")
                : t("settings.storage.saveLimit")}
            </Button>
          </CardFooter>
        </Card>

        <Card>
          <CardHeader className="border-b">
            <CardTitle>{t("settings.preferences.title")}</CardTitle>
            <CardDescription>
              {t("settings.preferences.description")}
            </CardDescription>
          </CardHeader>
          <CardContent className="px-0 py-0">
            <SettingsPreferenceRow
              control={
                <LocaleToggle
                  className="min-w-20 justify-between self-start"
                  dropdownClassName="min-w-30"
                  size="default"
                />
              }
              description={t("settings.preferences.localeDescription")}
              label={t("settings.preferences.localeLabel")}
            />
            <Separator />
            <SettingsPreferenceRow
              control={
                <ThemeToggle
                  className="min-w-20 justify-between self-start"
                  size="default"
                />
              }
              description={t("settings.preferences.themeDescription")}
              label={t("settings.preferences.themeLabel")}
            />
          </CardContent>
        </Card>
      </div>
    </section>
  );
}

function StorageMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="space-y-1 rounded-lg border border-border/70 bg-muted/40 p-3">
      <p className="text-sm text-muted-foreground">{label}</p>
      <p className="break-all text-sm font-medium">{value}</p>
    </div>
  );
}

function SettingsPreferenceRow({
  control,
  description,
  label,
}: {
  control: ReactNode;
  description: string;
  label: string;
}) {
  return (
    <div className="flex flex-col gap-3 px-4 py-2.5 first:pt-2.5 last:pb-2.5 sm:flex-row sm:items-center sm:justify-between sm:gap-6">
      <div className="space-y-1">
        <p className="text-sm font-medium">{label}</p>
        <p className="text-sm text-muted-foreground">{description}</p>
      </div>
      <div className="self-start">{control}</div>
    </div>
  );
}

function bytesToGiBString(value: number) {
  return (value / 1024 ** 3).toFixed(1);
}

function parseGiBToBytes(value: string) {
  const parsed = Number.parseFloat(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return null;
  }

  return Math.round(parsed * 1024 ** 3);
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
