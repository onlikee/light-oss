import axios from "axios";
import { ApiError, apiRequest, createApiClient } from "./client";
import type { AxiosProgressEvent } from "axios";
import type { AppSettings } from "../lib/settings";
import type {
  BatchUploadResult,
  DeleteRecycleBinObjectsResult,
  DeleteExplorerEntriesBatchItem,
  DeleteExplorerEntriesBatchResult,
  ExplorerEntriesResult,
  ObjectItem,
  ObjectListResult,
  ObjectVisibility,
  RecycleBinObjectListResult,
  RestoreRecycleBinObjectsResult,
  SignedDownloadPathResult,
} from "./types";
import { buildFolderUploadManifest } from "../lib/folder-upload";
import type { ExplorerSortBy, ExplorerSortOrder } from "../lib/explorer";

export interface ListObjectsParams {
  bucket: string;
  prefix: string;
  limit: number;
  cursor: string;
}

export interface UploadObjectParams {
  bucket: string;
  objectKey: string;
  file: File;
  visibility: ObjectVisibility;
  allowOverwrite?: boolean;
  onProgress?: (value: number) => void;
}

export interface UploadFolderParams {
  bucket: string;
  prefix: string;
  files: File[];
  visibility: ObjectVisibility;
  allowOverwrite?: boolean;
  onProgress?: (value: number) => void;
}

export interface ListExplorerEntriesParams {
  bucket: string;
  prefix: string;
  search: string;
  limit: number;
  cursor: string;
  sortBy: ExplorerSortBy | "";
  sortOrder: ExplorerSortOrder | "";
}

export interface CreateFolderParams {
  bucket: string;
  prefix: string;
  name: string;
}

export interface DeleteFolderOptions {
  recursive?: boolean;
}

export interface UpdateObjectVisibilityParams {
  bucket: string;
  objectKey: string;
  visibility: ObjectVisibility;
}

export interface ListRecycleBinObjectsParams {
  bucket?: string;
  limit: number;
  cursor: string;
}

export function listObjects(settings: AppSettings, params: ListObjectsParams) {
  return apiRequest<ObjectListResult>(settings, {
    method: "GET",
    url: `/api/v1/buckets/${encodeURIComponent(params.bucket)}/objects`,
    params: {
      prefix: params.prefix || undefined,
      limit: params.limit,
      cursor: params.cursor || undefined,
    },
  });
}

export function listExplorerEntries(
  settings: AppSettings,
  params: ListExplorerEntriesParams,
) {
  return apiRequest<ExplorerEntriesResult>(settings, {
    method: "GET",
    url: `/api/v1/buckets/${encodeURIComponent(params.bucket)}/entries`,
    params: {
      prefix: params.prefix || undefined,
      search: params.search || undefined,
      limit: params.limit,
      cursor: params.cursor || undefined,
      sort_by: params.sortBy || undefined,
      sort_order: params.sortOrder || undefined,
    },
  });
}

export function createFolder(
  settings: AppSettings,
  params: CreateFolderParams,
) {
  return apiRequest(settings, {
    method: "POST",
    url: `/api/v1/buckets/${encodeURIComponent(params.bucket)}/folders`,
    data: {
      prefix: params.prefix,
      name: params.name,
    },
  });
}

export function deleteFolder(
  settings: AppSettings,
  bucket: string,
  folderPath: string,
  options?: DeleteFolderOptions,
) {
  return apiRequest<void>(settings, {
    method: "DELETE",
    url: `/api/v1/buckets/${encodeURIComponent(bucket)}/folders`,
    params: {
      path: folderPath,
      recursive: options?.recursive ? true : undefined,
    },
  });
}

export async function checkObjectExists(
  settings: AppSettings,
  bucket: string,
  objectKey: string,
) {
  try {
    await createApiClient(settings).request({
      method: "HEAD",
      url: `/api/v1/buckets/${encodeURIComponent(bucket)}/objects/${encodeObjectKey(
        objectKey,
      )}`,
    });

    return true;
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 404) {
      return false;
    }

    throw normalizeRequestError(error, "Failed to check object existence");
  }
}

export async function uploadObject(
  settings: AppSettings,
  params: UploadObjectParams,
) {
  try {
    const response = await createApiClient(settings).request({
      method: "PUT",
      url: `/api/v1/buckets/${encodeURIComponent(params.bucket)}/objects/${encodeObjectKey(
        params.objectKey,
      )}`,
      timeout: 0,
      data: params.file,
      headers: {
        "Content-Type": resolveUploadContentType(params.file),
        "X-Object-Visibility": params.visibility,
        "X-Original-Filename": encodeHeaderFilename(params.file.name),
        "X-Allow-Overwrite": params.allowOverwrite ? "true" : "false",
      },
      onUploadProgress: (event: AxiosProgressEvent) => {
        if (!params.onProgress || !event.total) {
          return;
        }
        params.onProgress(Math.round((event.loaded / event.total) * 100));
      },
    });

    return response.data.data as ObjectItem;
  } catch (error) {
    throw normalizeRequestError(error, "Upload failed");
  }
}

export async function uploadFolder(
  settings: AppSettings,
  params: UploadFolderParams,
) {
  if (params.files.length === 0) {
    throw new Error("No files selected");
  }

  const formData = new FormData();
  if (params.prefix.trim() !== "") {
    formData.append("prefix", params.prefix);
  }
  formData.append("visibility", params.visibility);

  const manifest = buildFolderUploadManifest(params.files);
  manifest.forEach((item, index) => {
    formData.append(
      item.file_field,
      params.files[index],
      params.files[index].name,
    );
  });
  formData.append("manifest", JSON.stringify(manifest));

  try {
    const response = await createApiClient(settings).request({
      method: "POST",
      url: `/api/v1/buckets/${encodeURIComponent(params.bucket)}/objects/batch`,
      timeout: 0,
      data: formData,
      headers: {
        "X-Allow-Overwrite": params.allowOverwrite ? "true" : "false",
      },
      onUploadProgress: (event: AxiosProgressEvent) => {
        if (!params.onProgress || !event.total) {
          return;
        }
        params.onProgress(Math.round((event.loaded / event.total) * 100));
      },
    });

    return response.data.data as BatchUploadResult;
  } catch (error) {
    throw normalizeRequestError(error, "Upload failed");
  }
}

export function deleteObject(
  settings: AppSettings,
  bucket: string,
  objectKey: string,
) {
  return apiRequest<void>(settings, {
    method: "DELETE",
    url: `/api/v1/buckets/${encodeURIComponent(bucket)}/objects/${encodeObjectKey(objectKey)}`,
  });
}

export function deleteExplorerEntriesBatch(
  settings: AppSettings,
  bucket: string,
  items: DeleteExplorerEntriesBatchItem[],
) {
  return apiRequest<DeleteExplorerEntriesBatchResult>(settings, {
    method: "POST",
    url: `/api/v1/buckets/${encodeURIComponent(bucket)}/entries/batch-delete`,
    data: {
      items,
    },
  });
}

export function listRecycleBinObjects(
  settings: AppSettings,
  params: ListRecycleBinObjectsParams,
) {
  return apiRequest<RecycleBinObjectListResult>(settings, {
    method: "GET",
    url: "/api/v1/recycle-bin/objects",
    params: {
      bucket: params.bucket || undefined,
      limit: params.limit,
      cursor: params.cursor || undefined,
    },
  });
}

export function restoreRecycleBinObjects(
  settings: AppSettings,
  itemIds: number[],
) {
  return apiRequest<RestoreRecycleBinObjectsResult>(settings, {
    method: "POST",
    url: "/api/v1/recycle-bin/objects/restore",
    data: {
      item_ids: itemIds,
    },
  });
}

export function deleteRecycleBinObjects(
  settings: AppSettings,
  itemIds: number[],
) {
  return apiRequest<DeleteRecycleBinObjectsResult>(settings, {
    method: "POST",
    url: "/api/v1/recycle-bin/objects/batch-delete",
    data: {
      item_ids: itemIds,
    },
  });
}

export function updateObjectVisibility(
  settings: AppSettings,
  params: UpdateObjectVisibilityParams,
) {
  return apiRequest<ObjectItem>(settings, {
    method: "PATCH",
    url: `/api/v1/buckets/${encodeURIComponent(params.bucket)}/objects/visibility/${encodeObjectKey(
      params.objectKey,
    )}`,
    data: {
      visibility: params.visibility,
    },
  });
}

export function createSignedDownloadPath(
  settings: AppSettings,
  bucket: string,
  objectKey: string,
  expiresInSeconds?: number,
) {
  return apiRequest<SignedDownloadPathResult>(settings, {
    method: "POST",
    url: "/api/v1/sign/download",
    data: {
      bucket,
      object_key: objectKey,
      ...(expiresInSeconds === undefined
        ? {}
        : { expires_in_seconds: expiresInSeconds }),
    },
  });
}

export async function downloadFolderZip(
  settings: AppSettings,
  bucket: string,
  folderPath: string,
) {
  const fallbackFilename = buildFolderArchiveFallbackName(folderPath);

  try {
    const response = await createApiClient(settings).request<Blob>({
      method: "GET",
      url: `/api/v1/buckets/${encodeURIComponent(bucket)}/folders/archive`,
      timeout: 0,
      params: {
        path: folderPath,
      },
      responseType: "blob",
    });

    const blobUrl = URL.createObjectURL(response.data);
    const filename = getDownloadFilename(
      response.headers["content-disposition"],
      fallbackFilename,
    );

    try {
      const link = document.createElement("a");
      link.href = blobUrl;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
    } finally {
      URL.revokeObjectURL(blobUrl);
    }
  } catch (error) {
    throw await normalizeBlobDownloadError(error, "Folder ZIP download failed");
  }
}

export function buildPublicObjectURL(
  apiBaseUrl: string,
  bucket: string,
  objectKey: string,
  options?: {
    download?: boolean;
  },
) {
  const url = buildApiURL(
    apiBaseUrl,
    `/api/v1/buckets/${encodeURIComponent(bucket)}/objects/${encodeObjectKey(objectKey)}`,
  );

  if (!options?.download) {
    return url;
  }

  return `${url}?download=true`;
}

export function buildApiURL(apiBaseUrl: string, path: string) {
  return `${apiBaseUrl.trim().replace(/\/+$/, "")}${path}`;
}

function encodeObjectKey(objectKey: string) {
  return objectKey.split("/").map(encodeObjectKeySegment).join("/");
}

function encodeHeaderFilename(filename: string) {
  return encodeURIComponent(filename);
}

function resolveUploadContentType(file: File) {
  const detectedType = file.type.trim();
  if (detectedType) {
    return detectedType;
  }

  if (file.name.toLowerCase().endsWith(".md")) {
    return "text/markdown";
  }

  return "application/octet-stream";
}

function buildFolderArchiveFallbackName(folderPath: string) {
  const trimmed = folderPath.replace(/\/+$/, "");
  const segments = trimmed.split("/").filter(Boolean);
  const leaf = segments.length > 0 ? segments[segments.length - 1] : "folder";
  return `${leaf}.zip`;
}

function encodeObjectKeySegment(segment: string) {
  // Keep dots percent-encoded so upstream proxies do not treat object keys as file extensions.
  return encodeURIComponent(segment).replace(/\./g, "%2E");
}

function getDownloadFilename(
  contentDisposition: string | undefined,
  fallbackFilename: string,
) {
  if (!contentDisposition) {
    return fallbackFilename;
  }

  const utf8Match = contentDisposition.match(
    /filename\*\s*=\s*UTF-8''([^;]+)/i,
  );
  if (utf8Match?.[1]) {
    try {
      return decodeURIComponent(utf8Match[1]);
    } catch {
      return fallbackFilename;
    }
  }

  const filenameMatch = contentDisposition.match(/filename\s*=\s*"([^"]+)"/i);
  if (filenameMatch?.[1]) {
    return filenameMatch[1];
  }

  const unquotedMatch = contentDisposition.match(/filename\s*=\s*([^;]+)/i);
  if (unquotedMatch?.[1]) {
    return unquotedMatch[1].trim();
  }

  return fallbackFilename;
}

async function normalizeBlobDownloadError(
  error: unknown,
  fallbackMessage: string,
) {
  if (axios.isAxiosError(error)) {
    const status = error.response?.status ?? 500;
    const payload = error.response?.data;

    if (payload instanceof Blob) {
      try {
        const parsed = JSON.parse(await payload.text()) as {
          error?: { code?: string; message?: string };
        };
        return new ApiError(
          parsed.error?.message ?? error.message ?? fallbackMessage,
          status,
          parsed.error?.code,
        );
      } catch {
        return new ApiError(error.message || fallbackMessage, status);
      }
    }

    const parsed =
      payload && typeof payload === "object"
        ? (payload as { error?: { code?: string; message?: string } })
        : undefined;
    return new ApiError(
      parsed?.error?.message ?? error.message ?? fallbackMessage,
      status,
      parsed?.error?.code,
    );
  }

  if (error instanceof Error) {
    return new ApiError(error.message, 500);
  }

  return new ApiError(fallbackMessage, 500);
}

function normalizeRequestError(error: unknown, fallbackMessage: string) {
  if (error instanceof ApiError) {
    return error;
  }

  if (axios.isAxiosError(error)) {
    const status = error.response?.status ?? 500;
    const parsed =
      error.response?.data && typeof error.response.data === "object"
        ? (error.response.data as {
            error?: { code?: string; message?: string };
          })
        : undefined;

    return new ApiError(
      parsed?.error?.message ?? error.message ?? fallbackMessage,
      status,
      parsed?.error?.code,
    );
  }

  if (error instanceof Error) {
    return new ApiError(error.message, 500);
  }

  return new ApiError(fallbackMessage, 500);
}
