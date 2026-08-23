package model

import "time"

type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

type Bucket struct {
	ID        uint64    `gorm:"primaryKey"`
	Name      string    `gorm:"size:128;uniqueIndex;not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

type SystemStorageQuota struct {
	ID            uint64     `gorm:"primaryKey"`
	MaxBytes      uint64     `gorm:"not null"`
	UsedBytes     uint64     `gorm:"not null;default:0"`
	ReservedBytes uint64     `gorm:"not null;default:0"`
	ReconciledAt  *time.Time `gorm:"index"`
	StorageID     *string    `gorm:"size:36"`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
}

func (SystemStorageQuota) TableName() string {
	return "system_storage_quotas"
}

type StorageBlobStatus string

const (
	StorageBlobStatusStaging       StorageBlobStatus = "staging"
	StorageBlobStatusActive        StorageBlobStatus = "active"
	StorageBlobStatusPendingDelete StorageBlobStatus = "pending_delete"
	StorageBlobStatusOrphaned      StorageBlobStatus = "orphaned"
)

type StorageBlob struct {
	ID                    string            `gorm:"primaryKey;size:36;index:idx_storage_blobs_status_created,priority:3;index:idx_storage_blobs_staging_lease,priority:3"`
	StoragePath           string            `gorm:"size:512;not null;uniqueIndex"`
	StagingPath           *string           `gorm:"size:512"`
	Size                  uint64            `gorm:"not null;default:0"`
	RefCount              uint64            `gorm:"not null;default:0"`
	Status                StorageBlobStatus `gorm:"size:32;not null;index:idx_storage_blobs_status_created,priority:1;index:idx_storage_blobs_staging_lease,priority:1"`
	StagingLeaseExpiresAt *time.Time        `gorm:"index:idx_storage_blobs_staging_lease,priority:2"`
	CreatedAt             time.Time         `gorm:"not null;index:idx_storage_blobs_status_created,priority:2"`
	UpdatedAt             time.Time         `gorm:"not null"`
}

type StorageCleanupJob struct {
	ID             uint64       `gorm:"primaryKey"`
	BlobID         string       `gorm:"size:36;not null;uniqueIndex"`
	StoragePath    string       `gorm:"size:512;not null"`
	RetryCount     uint         `gorm:"not null;default:0"`
	NextRetryAt    time.Time    `gorm:"not null;index"`
	LastError      string       `gorm:"type:text;not null"`
	LeaseOwner     *string      `gorm:"size:128"`
	LeaseExpiresAt *time.Time   `gorm:"index"`
	CreatedAt      time.Time    `gorm:"not null"`
	UpdatedAt      time.Time    `gorm:"not null"`
	Blob           *StorageBlob `json:"-" gorm:"foreignKey:BlobID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

type Site struct {
	ID            uint64       `gorm:"primaryKey"`
	BucketName    string       `gorm:"size:128;not null;index:idx_sites_bucket_name"`
	RootPrefix    string       `gorm:"size:512;not null"`
	Enabled       bool         `gorm:"not null;default:true"`
	IndexDocument string       `gorm:"size:255;not null"`
	ErrorDocument string       `gorm:"size:255;not null;default:''"`
	SPAFallback   bool         `gorm:"not null;default:false"`
	CreatedAt     time.Time    `gorm:"not null"`
	UpdatedAt     time.Time    `gorm:"not null"`
	Bucket        *Bucket      `json:"-" gorm:"foreignKey:BucketName;references:Name;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Domains       []SiteDomain `gorm:"foreignKey:SiteID"`
}

type SiteDomain struct {
	ID        uint64    `gorm:"primaryKey"`
	SiteID    uint64    `gorm:"not null;index:idx_site_domains_site_id"`
	Domain    string    `gorm:"size:255;not null;uniqueIndex:udx_site_domains_domain"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	Site      *Site     `json:"-" gorm:"foreignKey:SiteID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

type Object struct {
	ID               uint64     `gorm:"primaryKey"`
	BucketName       string     `gorm:"size:128;not null;uniqueIndex:udx_objects_bucket_key,priority:1;index:idx_objects_bucket_created,priority:1;index:idx_objects_bucket_key,priority:1"`
	ObjectKey        string     `gorm:"size:512;not null;uniqueIndex:udx_objects_bucket_key,priority:2;index:idx_objects_bucket_key,priority:3"`
	OriginalFilename string     `gorm:"size:255;not null"`
	StoragePath      string     `gorm:"size:512;not null"`
	Size             int64      `gorm:"not null"`
	ContentType      string     `gorm:"size:255;not null"`
	ETag             string     `gorm:"column:etag;size:64;not null"`
	FileFingerprint  *string    `gorm:"column:file_fingerprint;size:64;index:idx_objects_bucket_fingerprint,priority:3"`
	Visibility       Visibility `gorm:"size:16;not null"`
	IsDeleted        bool       `gorm:"not null;default:false;index:idx_objects_bucket_created,priority:2;index:idx_objects_bucket_key,priority:2;index:idx_objects_bucket_fingerprint,priority:2"`
	CreatedAt        time.Time  `gorm:"not null;index:idx_objects_bucket_created,priority:3,sort:desc"`
	UpdatedAt        time.Time  `gorm:"not null"`
	Bucket           *Bucket    `json:"-" gorm:"foreignKey:BucketName;references:Name;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

type RecycleBinObject struct {
	ID               uint64     `gorm:"primaryKey"`
	DeleteGroupID    string     `gorm:"column:delete_group_id;size:36;not null;index:idx_recycle_bin_objects_delete_group"`
	BucketName       string     `gorm:"size:128;not null;index:idx_recycle_bin_objects_deleted,priority:3;index:idx_recycle_bin_objects_bucket"`
	ObjectKey        string     `gorm:"size:512;not null"`
	OriginalFilename string     `gorm:"size:255;not null"`
	StoragePath      string     `gorm:"size:512;not null;index:idx_recycle_bin_objects_storage_path"`
	Size             int64      `gorm:"not null"`
	ContentType      string     `gorm:"size:255;not null"`
	ETag             string     `gorm:"column:etag;size:64;not null"`
	FileFingerprint  *string    `gorm:"column:file_fingerprint;size:64"`
	Visibility       Visibility `gorm:"size:16;not null"`
	CreatedAt        time.Time  `gorm:"not null"`
	DeletedAt        time.Time  `gorm:"not null;index:idx_recycle_bin_objects_deleted,priority:1,sort:desc"`
}
