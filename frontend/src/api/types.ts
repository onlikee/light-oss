export type ObjectVisibility = "public" | "private";

export interface ApiErrorBody {
  code: string;
  message: string;
}

export interface ApiEnvelope<T> {
  request_id: string;
  data: T;
  error?: ApiErrorBody;
}

export interface Bucket {
  id: number;
  name: string;
  created_at: string;
  updated_at: string;
}

export interface BucketListResult {
  items: Bucket[];
}

export interface ObjectItem {
  id: number;
  bucket_name: string;
  object_key: string;
  original_filename: string;
  size: number;
  content_type: string;
  etag: string;
  visibility: ObjectVisibility;
  created_at: string;
  updated_at: string;
}

export interface ObjectListResult {
  items: ObjectItem[];
  next_cursor: string;
}

export interface ExplorerDirectoryEntry {
  type: "directory";
  path: string;
  name: string;
  is_empty: boolean | null;
  object_key: null;
  original_filename: null;
  size: null;
  content_type: null;
  etag: null;
  visibility: null;
  created_at?: null;
  updated_at: null;
}

export interface ExplorerFileEntry {
  type: "file";
  path: string;
  name: string;
  is_empty: null;
  object_key: string;
  original_filename: string;
  size: number;
  content_type: string;
  etag: string;
  visibility: ObjectVisibility;
  created_at?: string;
  updated_at: string;
}

export type ExplorerEntry = ExplorerDirectoryEntry | ExplorerFileEntry;

export interface ExplorerEntriesResult {
  items: ExplorerEntry[];
  next_cursor: string;
}

export interface DeleteExplorerEntriesBatchItem {
  type: "file" | "directory";
  path: string;
}

export interface DeleteExplorerEntriesBatchFailedItem extends DeleteExplorerEntriesBatchItem {
  code: string;
  message: string;
}

export interface DeleteExplorerEntriesBatchResult {
  deleted_count: number;
  failed_count: number;
  failed_items: DeleteExplorerEntriesBatchFailedItem[];
}

export type RecycleBinObjectType = "file" | "directory";

export interface RecycleBinObjectItem {
  id: number;
  type: RecycleBinObjectType;
  bucket_name: string;
  path: string;
  name: string;
  object_key: string;
  original_filename: string;
  size: number;
  content_type: string;
  etag: string;
  visibility: ObjectVisibility;
  created_at: string;
  deleted_at: string;
}

export interface RecycleBinObjectListResult {
  items: RecycleBinObjectItem[];
  next_cursor: string;
}

export interface RecycleBinFailedItem {
  id: number;
  bucket_name: string;
  path: string;
  code: string;
  message: string;
}

export interface RestoreRecycleBinObjectsResult {
  restored_count: number;
  failed_count: number;
  failed_items: RecycleBinFailedItem[];
}

export interface DeleteRecycleBinObjectsResult {
  deleted_count: number;
  failed_count: number;
  failed_items: RecycleBinFailedItem[];
}

export interface BatchUploadResult {
  uploaded_count: number;
  items: ObjectItem[];
}

export interface Site {
  id: number;
  bucket: string;
  root_prefix: string;
  enabled: boolean;
  index_document: string;
  error_document: string;
  spa_fallback: boolean;
  domains: string[];
  created_at: string;
  updated_at: string;
}

export interface SiteListResult {
  items: Site[];
}

export interface CreateSiteRequest {
  bucket: string;
  root_prefix: string;
  enabled: boolean;
  index_document: string;
  error_document: string;
  spa_fallback: boolean;
  domains: string[];
}

export type UpdateSiteRequest = CreateSiteRequest;

export interface PublishSiteResult {
  uploaded_count: number;
  site: Site;
}

export interface SignedDownloadPathResult {
  path: string;
  expires_at: number;
}

export type HealthState = "ok" | "error";

export interface HealthStatusResult {
  status: {
    service: HealthState;
    db: HealthState;
  };
  version: string;
}

export type ApiHostOS = "windows" | "linux" | "macos" | "other";

export interface SystemCPUStats {
  used_percent: number;
}

export interface SystemMemoryStats {
  total_bytes: number;
  used_bytes: number;
  available_bytes: number;
  used_percent: number;
}

export interface SystemDiskStats {
  label: string;
  mount_point: string;
  filesystem: string;
  total_bytes: number;
  used_bytes: number;
  free_bytes: number;
  used_percent: number;
  contains_storage_root: boolean;
}

export type StorageLimitStatus = "ok" | "warning" | "exceeded";

export interface SystemStorageStats {
  root_path: string;
  used_bytes: number;
  max_bytes: number;
  remaining_bytes: number;
  used_percent: number;
  limit_status: StorageLimitStatus;
}

export interface SystemStatsResult {
  os: ApiHostOS;
  cpu: SystemCPUStats;
  memory: SystemMemoryStats;
  disks: SystemDiskStats[];
  storage: SystemStorageStats;
}
