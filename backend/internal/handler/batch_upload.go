package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/pkg/response"
	"light-oss/backend/internal/service"
)

type uploadBatchManifestItemRequest struct {
	FileField    string `json:"file_field"`
	RelativePath string `json:"relative_path"`
}

type uploadBatchResponse struct {
	UploadedCount int              `json:"uploaded_count"`
	Items         []objectResponse `json:"items"`
}

type streamedUploadBatchFile struct {
	Filename    string
	ContentType string
	TempPath    string
	Size        uint64
	PartCount   int
}

func (h *apiHandler) uploadObjectBatch(c *gin.Context) {
	reader, err := c.Request.MultipartReader()
	if err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_multipart_request", "multipart form is invalid"))
		return
	}

	tempDir, err := os.MkdirTemp("", "light-oss-batch-*")
	if err != nil {
		response.Error(c, apperrors.Wrap(http.StatusInternalServerError, "batch_file_buffer_failed", "failed to buffer uploaded files", err))
		return
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	fileParts, formValues, err := readBatchMultipartRequest(reader, tempDir)
	if err != nil {
		response.Error(c, err)
		return
	}

	manifest, err := parseUploadBatchManifestValue(formValues["manifest"])
	if err != nil {
		response.Error(c, err)
		return
	}

	prefix := strings.TrimSpace(formValues["prefix"])
	visibility := strings.TrimSpace(formValues["visibility"])
	allowOverwrite, err := parseOptionalBool(c.GetHeader("X-Allow-Overwrite"))
	if err != nil {
		response.Error(c, apperrors.New(http.StatusBadRequest, "invalid_request", "X-Allow-Overwrite must be true or false"))
		return
	}

	items, err := buildUploadBatchItemsFromManifest(fileParts, manifest)
	if err != nil {
		response.Error(c, err)
		return
	}

	result, err := h.objectService.UploadBatch(c.Request.Context(), service.UploadObjectBatchInput{
		BucketName:     c.Param("bucket"),
		Prefix:         prefix,
		Visibility:     visibility,
		AllowOverwrite: allowOverwrite,
		Items:          items,
	})
	if err != nil {
		response.Error(c, err)
		return
	}

	objects := make([]objectResponse, 0, len(result.Items))
	for _, item := range result.Items {
		objects = append(objects, objectToResponse(item))
	}

	response.JSON(c, http.StatusCreated, uploadBatchResponse{
		UploadedCount: result.UploadedCount,
		Items:         objects,
	})
}

func buildUploadBatchItemsFromManifest(
	fileParts map[string]*streamedUploadBatchFile,
	manifest []uploadBatchManifestItemRequest,
) ([]service.UploadObjectBatchItemInput, error) {
	seenFileFields := make(map[string]struct{}, len(manifest))
	items := make([]service.UploadObjectBatchItemInput, 0, len(manifest))

	for _, entry := range manifest {
		fileField := strings.TrimSpace(entry.FileField)
		relativePath := strings.TrimSpace(entry.RelativePath)
		if fileField == "" || relativePath == "" {
			return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest entry is invalid")
		}
		if _, exists := seenFileFields[fileField]; exists {
			return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest contains duplicate file fields")
		}
		seenFileFields[fileField] = struct{}{}

		filePart, err := getSingleStreamedMultipartFile(fileParts, fileField)
		if err != nil {
			return nil, err
		}

		currentFilePart := filePart
		originalFilename := strings.TrimSpace(currentFilePart.Filename)
		if originalFilename == "" {
			originalFilename = path.Base(relativePath)
		}

		items = append(items, service.UploadObjectBatchItemInput{
			RelativePath:     relativePath,
			OriginalFilename: originalFilename,
			ContentType:      currentFilePart.ContentType,
			Size:             &currentFilePart.Size,
			Open: func() (io.ReadCloser, error) {
				file, err := os.Open(currentFilePart.TempPath)
				if err != nil {
					return nil, err
				}

				return file, nil
			},
		})
	}

	return items, nil
}

func readBatchMultipartRequest(
	reader *multipart.Reader,
	tempDir string,
) (map[string]*streamedUploadBatchFile, map[string]string, error) {
	fileParts := make(map[string]*streamedUploadBatchFile)
	formValues := make(map[string]string)

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isRequestBodyTooLarge(err) {
				return nil, nil, apperrors.New(http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds configured upload size")
			}

			return nil, nil, apperrors.New(http.StatusBadRequest, "invalid_multipart_request", "multipart form is invalid")
		}

		formName := strings.TrimSpace(part.FormName())
		if formName == "" {
			if err := drainMultipartPart(part); err != nil {
				return nil, nil, err
			}
			continue
		}

		if part.FileName() == "" {
			value, err := readMultipartFieldValue(part)
			if err != nil {
				return nil, nil, err
			}
			if _, exists := formValues[formName]; !exists {
				formValues[formName] = value
			}
			continue
		}

		filename := part.FileName()
		contentType := part.Header.Get("Content-Type")
		tempPath, size, err := writeMultipartFilePart(tempDir, part)
		if err != nil {
			return nil, nil, err
		}

		filePart, exists := fileParts[formName]
		if !exists {
			fileParts[formName] = &streamedUploadBatchFile{
				Filename:    filename,
				ContentType: contentType,
				TempPath:    tempPath,
				Size:        size,
				PartCount:   1,
			}
			continue
		}

		filePart.PartCount++
	}

	return fileParts, formValues, nil
}

func parseUploadBatchManifestValue(rawManifest string) ([]uploadBatchManifestItemRequest, error) {
	if strings.TrimSpace(rawManifest) == "" {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest is required")
	}

	var manifest []uploadBatchManifestItemRequest
	if err := json.Unmarshal([]byte(rawManifest), &manifest); err != nil {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest is invalid")
	}
	if len(manifest) == 0 {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest must contain at least one file")
	}

	return manifest, nil
}

func getSingleStreamedMultipartFile(
	fileParts map[string]*streamedUploadBatchFile,
	fileField string,
) (*streamedUploadBatchFile, error) {
	filePart, exists := fileParts[fileField]
	if !exists {
		return nil, apperrors.New(http.StatusBadRequest, "batch_file_missing", "manifest references a missing file")
	}
	if filePart.PartCount > 1 {
		return nil, apperrors.New(http.StatusBadRequest, "invalid_batch_manifest", "manifest must reference exactly one file per field")
	}

	return filePart, nil
}

func readMultipartFieldValue(part *multipart.Part) (string, error) {
	rawValue, err := io.ReadAll(part)
	if err != nil {
		if isRequestBodyTooLarge(err) {
			return "", apperrors.New(http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds configured upload size")
		}

		return "", apperrors.New(http.StatusBadRequest, "invalid_multipart_request", "multipart form is invalid")
	}
	if err := part.Close(); err != nil {
		return "", apperrors.New(http.StatusBadRequest, "invalid_multipart_request", "multipart form is invalid")
	}

	return strings.TrimSpace(string(rawValue)), nil
}

func writeMultipartFilePart(tempDir string, part *multipart.Part) (string, uint64, error) {
	tempFile, err := os.CreateTemp(tempDir, "part-*")
	if err != nil {
		_ = part.Close()
		return "", 0, apperrors.Wrap(http.StatusInternalServerError, "batch_file_buffer_failed", "failed to buffer uploaded files", err)
	}

	tempPath := tempFile.Name()
	size, err := io.Copy(tempFile, part)
	if err != nil {
		_ = tempFile.Close()
		_ = part.Close()
		_ = os.Remove(tempPath)
		if isRequestBodyTooLarge(err) {
			return "", 0, apperrors.New(http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds configured upload size")
		}

		return "", 0, apperrors.New(http.StatusBadRequest, "invalid_multipart_request", "multipart form is invalid")
	}
	if err := tempFile.Close(); err != nil {
		_ = part.Close()
		_ = os.Remove(tempPath)
		return "", 0, apperrors.Wrap(http.StatusInternalServerError, "batch_file_buffer_failed", "failed to buffer uploaded files", err)
	}
	if err := part.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", 0, apperrors.New(http.StatusBadRequest, "invalid_multipart_request", "multipart form is invalid")
	}

	return tempPath, uint64(size), nil
}

func drainMultipartPart(part *multipart.Part) error {
	if _, err := io.Copy(io.Discard, part); err != nil {
		_ = part.Close()
		if isRequestBodyTooLarge(err) {
			return apperrors.New(http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds configured upload size")
		}

		return apperrors.New(http.StatusBadRequest, "invalid_multipart_request", "multipart form is invalid")
	}
	if err := part.Close(); err != nil {
		return apperrors.New(http.StatusBadRequest, "invalid_multipart_request", "multipart form is invalid")
	}

	return nil
}

func isRequestBodyTooLarge(err error) bool {
	return err != nil && strings.Contains(err.Error(), "http: request body too large")
}
