export const explorerPageSizes = [10, 20, 50, 100, 200] as const;
export const explorerSortByValues = ["name", "size", "created_at"] as const;
export const explorerSortOrderValues = ["asc", "desc"] as const;

export type ExplorerSortBy = (typeof explorerSortByValues)[number];
export type ExplorerSortOrder = (typeof explorerSortOrderValues)[number];

export function normalizeExplorerPrefix(value: string | null | undefined) {
  const trimmed = (value ?? "").trim().replace(/^\/+/, "").replace(/\/+$/, "");
  return trimmed ? `${trimmed}/` : "";
}

export function normalizeExplorerSearch(value: string | null | undefined) {
  return (value ?? "").trim();
}

export function parseExplorerLimit(value: string | null | undefined) {
  const limit = Number(value ?? "");
  if (explorerPageSizes.includes(limit as (typeof explorerPageSizes)[number])) {
    return limit as (typeof explorerPageSizes)[number];
  }

  return 20;
}

export function normalizeExplorerSortBy(value: string | null | undefined) {
  if (
    explorerSortByValues.includes(
      value as (typeof explorerSortByValues)[number],
    )
  ) {
    return value as ExplorerSortBy;
  }

  return null;
}

export function normalizeExplorerSortOrder(value: string | null | undefined) {
  if (
    explorerSortOrderValues.includes(
      value as (typeof explorerSortOrderValues)[number],
    )
  ) {
    return value as ExplorerSortOrder;
  }

  return null;
}

export function getNextExplorerSort(
  currentSortBy: ExplorerSortBy | null,
  currentSortOrder: ExplorerSortOrder | null,
  targetSortBy: ExplorerSortBy,
) {
  if (currentSortBy !== targetSortBy) {
    return {
      sortBy: targetSortBy,
      sortOrder: "asc" as ExplorerSortOrder,
    };
  }

  if (currentSortOrder === "asc") {
    return {
      sortBy: targetSortBy,
      sortOrder: "desc" as ExplorerSortOrder,
    };
  }

  return {
    sortBy: null,
    sortOrder: null,
  };
}

export function getExplorerBreadcrumbs(prefix: string) {
  const normalized = normalizeExplorerPrefix(prefix);
  if (!normalized) {
    return [];
  }

  const segments = normalized.split("/").filter(Boolean);
  return segments.map((segment, index) => ({
    label: segment,
    prefix: `${segments.slice(0, index + 1).join("/")}/`,
  }));
}

export function getParentExplorerPrefix(prefix: string) {
  const normalized = normalizeExplorerPrefix(prefix);
  if (!normalized) {
    return "";
  }

  const segments = normalized.split("/").filter(Boolean);
  if (segments.length <= 1) {
    return "";
  }

  return `${segments.slice(0, -1).join("/")}/`;
}

export function joinExplorerPath(prefix: string, name: string) {
  const normalizedPrefix = normalizeExplorerPrefix(prefix);
  const normalizedName = name.trim().replace(/^\/+/, "").replace(/\/+$/, "");
  return normalizedName
    ? `${normalizedPrefix}${normalizedName}`
    : normalizedPrefix;
}

export function isExplorerPrefixAncestor(ancestor: string, current: string) {
  const normalizedAncestor = normalizeExplorerPrefix(ancestor);
  const normalizedCurrent = normalizeExplorerPrefix(current);
  if (!normalizedAncestor) {
    return true;
  }

  return normalizedCurrent.startsWith(normalizedAncestor);
}
