import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { useI18n } from "@/lib/i18n";

function readPinnedIds(storageKey: string): number[] {
  const raw = window.localStorage.getItem(storageKey);
  if (!raw) return [];

  try {
    const parsed: unknown = JSON.parse(raw);
    if (
      !Array.isArray(parsed) ||
      !parsed.every((id) => Number.isSafeInteger(id) && id > 0)
    ) {
      return [];
    }
    return [...new Set<number>(parsed)];
  } catch {
    return [];
  }
}

function loadPins(storageKey: string) {
  try {
    return { storageKey, ids: readPinnedIds(storageKey), readFailed: false };
  } catch {
    return { storageKey, ids: [] as number[], readFailed: true };
  }
}

export function usePinnedBuckets(apiBaseUrl: string) {
  const { t } = useI18n();
  const storageError = t("buckets.pin.storageError");
  const storageKey = `light-oss-pinned-buckets:${apiBaseUrl.trim().replace(/\/+$/, "")}`;
  const [state, setState] = useState(() => loadPins(storageKey));

  // Reset during render so a service change never exposes the previous pins.
  if (state.storageKey !== storageKey) {
    setState(loadPins(storageKey));
  }

  useEffect(() => {
    if (state.readFailed) toast.error(storageError);
  }, [state.readFailed, state.storageKey, storageError]);

  const updatePins = useCallback(
    (update: (ids: number[]) => number[]) => {
      try {
        // Read at the time of the action, including after an async deletion.
        const current = readPinnedIds(storageKey);
        const next = update(current);
        if (
          next.length === current.length &&
          next.every((id, i) => id === current[i])
        )
          return;
        window.localStorage.setItem(storageKey, JSON.stringify(next));
        setState((previous) =>
          previous.storageKey === storageKey
            ? { storageKey, ids: next, readFailed: false }
            : previous,
        );
      } catch {
        toast.error(storageError);
      }
    },
    [storageKey, storageError],
  );

  const togglePin = useCallback(
    (id: number) => {
      updatePins((ids) =>
        ids.includes(id) ? ids.filter((value) => value !== id) : [...ids, id],
      );
    },
    [updatePins],
  );

  const removePin = useCallback(
    (id: number) => {
      updatePins((ids) => ids.filter((value) => value !== id));
    },
    [updatePins],
  );

  const reconcilePins = useCallback(
    (existingIds: number[]) => {
      const existing = new Set(existingIds);
      updatePins((ids) => ids.filter((id) => existing.has(id)));
    },
    [updatePins],
  );

  return { pinnedIds: state.ids, togglePin, removePin, reconcilePins };
}
