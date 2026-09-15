import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { deleteBucket } from "@/api/buckets";
import { useI18n } from "@/lib/i18n";
import { useAppSettings } from "@/lib/settings";

export function useDeleteBucket(removePin: (id: number) => void) {
  const { settings } = useAppSettings();
  const { t } = useI18n();
  const queryClient = useQueryClient();

  const mutation = useMutation({
    mutationFn: ({ name }: { name: string; id?: number }) =>
      deleteBucket(settings, name),
    onSuccess: async (_, { name, id }) => {
      if (id !== undefined) removePin(id);
      queryClient.removeQueries({
        queryKey: [
          "explorer-entries",
          settings.apiBaseUrl,
          settings.bearerToken,
          name,
        ],
      });
      toast.success(t("toast.bucketDeleted"));
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ["buckets", settings.apiBaseUrl, settings.bearerToken],
        }),
        queryClient.invalidateQueries({
          queryKey: ["sites", settings.apiBaseUrl, settings.bearerToken],
        }),
      ]);
    },
    onError: (error) => {
      toast.error(
        error instanceof Error ? error.message : t("errors.deleteBucket"),
      );
    },
  });

  return {
    deleteBucket: mutation.mutateAsync,
    deletingBucketName: mutation.isPending ? mutation.variables.name : "",
  };
}
