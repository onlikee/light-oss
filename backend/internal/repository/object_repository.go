package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"light-oss/backend/internal/model"
)

type Cursor struct {
	CreatedAt time.Time
	ID        uint64
}

type ListObjectsParams struct {
	BucketName string
	Prefix     string
	Limit      int
	Cursor     *Cursor
}

type ObjectRepository struct {
	db *gorm.DB
}

const objectKeyPrefixLikeClause = "object_key LIKE ? ESCAPE '!'"

func NewObjectRepository(db *gorm.DB) *ObjectRepository {
	return &ObjectRepository{db: db}
}

func (r *ObjectRepository) DB() *gorm.DB {
	return r.db
}

func (r *ObjectRepository) WithDB(db *gorm.DB) *ObjectRepository {
	if db == nil {
		return r
	}

	return &ObjectRepository{db: db}
}

func (r *ObjectRepository) Transaction(ctx context.Context, fn func(repo *ObjectRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(r.WithDB(tx))
	})
}

func (r *ObjectRepository) Upsert(ctx context.Context, object *model.Object) (*model.Object, error) {
	now := time.Now().UTC()
	object.CreatedAt = now
	object.UpdatedAt = now

	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "bucket_name"},
			{Name: "object_key"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"original_filename": object.OriginalFilename,
			"storage_path":      object.StoragePath,
			"size":              object.Size,
			"content_type":      object.ContentType,
			"etag":              object.ETag,
			"file_fingerprint":  object.FileFingerprint,
			"visibility":        object.Visibility,
			"created_at":        now,
			"updated_at":        now,
		}),
	}).Create(object).Error
	if err != nil {
		return nil, err
	}

	return r.FindActive(ctx, object.BucketName, object.ObjectKey)
}

func (r *ObjectRepository) Create(ctx context.Context, object *model.Object) (*model.Object, error) {
	now := time.Now().UTC()
	object.CreatedAt = now
	object.UpdatedAt = now
	if err := r.db.WithContext(ctx).Create(object).Error; err != nil {
		return nil, err
	}
	return object, nil
}

func (r *ObjectRepository) UpsertBatch(ctx context.Context, objects []model.Object, batchSize int) error {
	if len(objects) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 100
	}

	now := time.Now().UTC()
	for index := range objects {
		objects[index].CreatedAt = now
		objects[index].UpdatedAt = now
	}

	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "bucket_name"},
			{Name: "object_key"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"original_filename",
			"storage_path",
			"size",
			"content_type",
			"etag",
			"file_fingerprint",
			"visibility",
			"created_at",
			"updated_at",
		}),
	}).CreateInBatches(&objects, batchSize).Error
}

func (r *ObjectRepository) CreateBatch(ctx context.Context, objects []model.Object, batchSize int) error {
	if len(objects) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 100
	}

	now := time.Now().UTC()
	for index := range objects {
		objects[index].CreatedAt = now
		objects[index].UpdatedAt = now
	}
	return r.db.WithContext(ctx).CreateInBatches(&objects, batchSize).Error
}
