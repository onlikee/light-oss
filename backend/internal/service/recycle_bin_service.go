package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/repository"
)

type RecycleBinItemType string

const (
	RecycleBinItemTypeFile      RecycleBinItemType = "file"
	RecycleBinItemTypeDirectory RecycleBinItemType = "directory"
)

type RecycleBinObjectItem struct {
	ID               uint64
	Type             RecycleBinItemType
	BucketName       string
	Path             string
	Name             string
	ObjectKey        string
	OriginalFilename string
	Size             int64
	ContentType      string
	ETag             string
	Visibility       model.Visibility
	CreatedAt        time.Time
	DeletedAt        time.Time
}

type ListRecycleBinObjectsInput struct {
	BucketName string
	Limit      int
	Cursor     string
}

type ListRecycleBinObjectsOutput struct {
	Items      []RecycleBinObjectItem
	NextCursor string
}

type RecycleBinFailedItem struct {
	ID         uint64
	BucketName string
	Path       string
	Code       string
	Message    string
}

type RestoreRecycleBinObjectsOutput struct {
	RestoredCount int
	FailedCount   int
	FailedItems   []RecycleBinFailedItem
}

type DeleteRecycleBinObjectsOutput struct {
	DeletedCount int
	FailedCount  int
	FailedItems  []RecycleBinFailedItem
}

type recycleBinLogicalItem struct {
	Item       RecycleBinObjectItem
	LastCursor *repository.RecycleBinCursor
}

type recycleBinDirectoryKey struct {
	BucketName string
	Path       string
}

type RecycleBinService struct {
	gormDB      *gorm.DB
	bucketRepo  *repository.BucketRepository
	objectRepo  *repository.ObjectRepository
	recycleRepo *repository.RecycleBinRepository
	blobs       *BlobLifecycleService
}

func NewRecycleBinService(
	gormDB *gorm.DB,
	bucketRepo *repository.BucketRepository,
	objectRepo *repository.ObjectRepository,
	recycleRepo *repository.RecycleBinRepository,
	blobLifecycle *BlobLifecycleService,
) *RecycleBinService {
	return &RecycleBinService{
		gormDB:      gormDB,
		bucketRepo:  bucketRepo,
		objectRepo:  objectRepo,
		recycleRepo: recycleRepo,
		blobs:       blobLifecycle,
	}
}

func (s *RecycleBinService) ListObjects(ctx context.Context, input ListRecycleBinObjectsInput) (*ListRecycleBinObjectsOutput, error) {
	if strings.TrimSpace(input.BucketName) != "" {
		if err := ValidateBucketName(input.BucketName); err != nil {
			return nil, err
		}
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	cursor, err := decodeRecycleBinCursor(input.Cursor)
	if err != nil {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
	}

	batchSize := limit + 1
	if batchSize < defaultListLimit {
		batchSize = defaultListLimit
	}

	items := make([]RecycleBinObjectItem, 0, limit)
	queryCursor := cursor
	var pageLastCursor *repository.RecycleBinCursor
	pendingRawItems := make([]model.RecycleBinObject, 0, batchSize)

	for {
		rawItems, listErr := s.recycleRepo.List(ctx, repository.ListRecycleBinObjectsParams{
			BucketName: input.BucketName,
			Limit:      batchSize,
			Cursor:     queryCursor,
		})
		if listErr != nil {
			return nil, apperrors.Wrap(http.StatusInternalServerError, "recycle_bin_list_failed", "failed to list recycle bin objects", listErr)
		}
		if len(rawItems) > 0 {
			pendingRawItems = append(pendingRawItems, rawItems...)
			queryCursor = recycleBinCursorFromObject(rawItems[len(rawItems)-1])
		}

		completeRawItems, remainingRawItems := splitCompleteRecycleBinRawItems(pendingRawItems, len(rawItems) == 0)
		for _, logicalItem := range recycleBinLogicalItemsFromRawItems(completeRawItems) {
			if len(items) == limit {
				nextCursor := ""
				if pageLastCursor != nil {
					nextCursor = encodeRecycleBinCursor(pageLastCursor.DeletedAt, pageLastCursor.ID)
				}

				return &ListRecycleBinObjectsOutput{
					Items:      items,
					NextCursor: nextCursor,
				}, nil
			}

			items = append(items, logicalItem.Item)
			pageLastCursor = logicalItem.LastCursor
		}

		pendingRawItems = remainingRawItems
		if len(rawItems) == 0 {
			break
		}
	}

	return &ListRecycleBinObjectsOutput{
		Items:      items,
		NextCursor: "",
	}, nil
}

func (s *RecycleBinService) RestoreObjects(ctx context.Context, itemIDs []uint64) (*RestoreRecycleBinObjectsOutput, error) {
	normalizedIDs, err := validateRecycleBinItemIDs(itemIDs)
	if err != nil {
		return nil, err
	}

	result := &RestoreRecycleBinObjectsOutput{
		FailedItems: make([]RecycleBinFailedItem, 0),
	}

	for _, itemID := range normalizedIDs {
		failedItem, restoreErr := s.restoreObject(ctx, itemID)
		if restoreErr == nil {
			result.RestoredCount++
			continue
		}

		if failedItem.ID == 0 {
			failedItem.ID = itemID
		}

		appErr := apperrors.From(restoreErr)
		failedItem.Code = appErr.Code
		failedItem.Message = appErr.Message
		result.FailedItems = append(result.FailedItems, failedItem)
	}

	result.FailedCount = len(result.FailedItems)
	return result, nil
}

func (s *RecycleBinService) DeleteObjects(ctx context.Context, itemIDs []uint64) (*DeleteRecycleBinObjectsOutput, error) {
	normalizedIDs, err := validateRecycleBinItemIDs(itemIDs)
	if err != nil {
		return nil, err
	}

	result := &DeleteRecycleBinObjectsOutput{
		FailedItems: make([]RecycleBinFailedItem, 0),
	}
	for _, itemID := range normalizedIDs {
		failedItem, deleteErr := s.deleteObject(ctx, itemID)
		if deleteErr == nil {
			result.DeletedCount++
			continue
		}

		if failedItem.ID == 0 {
			failedItem.ID = itemID
		}

		appErr := apperrors.From(deleteErr)
		failedItem.Code = appErr.Code
		failedItem.Message = appErr.Message
		result.FailedItems = append(result.FailedItems, failedItem)
	}

	result.FailedCount = len(result.FailedItems)
	return result, nil
}
