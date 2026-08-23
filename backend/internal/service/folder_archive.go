package service

import (
	"archive/zip"
	"context"
	"io"
	"net/http"
	"path"
	"strings"

	"light-oss/backend/internal/model"
	apperrors "light-oss/backend/internal/pkg/errors"
)

type FolderArchive struct {
	Filename string
	writeFn  func(io.Writer) error
}

func (a *FolderArchive) StreamTo(w io.Writer) error {
	return a.writeFn(w)
}

func (s *ObjectService) OpenFolderArchive(
	ctx context.Context,
	bucketName string,
	folderPath string,
) (*FolderArchive, error) {
	if err := ValidateBucketName(bucketName); err != nil {
		return nil, err
	}
	if err := ValidateFolderPath(folderPath); err != nil {
		return nil, err
	}
	if err := s.ensureBucketExists(ctx, bucketName); err != nil {
		return nil, err
	}

	exists, err := s.objectRepo.ExistsActiveWithPrefix(ctx, bucketName, folderPath)
	if err != nil {
		return nil, apperrors.Wrap(500, "folder_lookup_failed", "failed to look up folder", err)
	}
	if !exists {
		return nil, apperrors.New(http.StatusNotFound, "folder_not_found", "folder not found")
	}

	objects, err := s.objectRepo.ListActiveByPrefixOrdered(ctx, bucketName, folderPath)
	if err != nil {
		return nil, apperrors.Wrap(500, "folder_archive_failed", "failed to prepare folder archive", err)
	}

	rootName := folderArchiveRootName(folderPath)
	return &FolderArchive{
		Filename: folderArchiveFilename(rootName),
		writeFn: func(w io.Writer) error {
			return s.writeFolderArchive(ctx, w, rootName, folderPath, objects)
		},
	}, nil
}

func (s *ObjectService) writeFolderArchive(
	ctx context.Context,
	w io.Writer,
	rootName string,
	folderPath string,
	objects []model.Object,
) error {
	zipWriter := zip.NewWriter(w)
	directories := map[string]struct{}{}

	if err := writeFolderArchiveDirectory(zipWriter, directories, rootName+"/"); err != nil {
		return apperrors.Wrap(500, "folder_archive_failed", "failed to write folder archive", err)
	}

	for _, object := range objects {
		if err := ctx.Err(); err != nil {
			_ = zipWriter.Close()
			return err
		}

		relativePath := strings.TrimPrefix(object.ObjectKey, folderPath)
		if relativePath == "" {
			continue
		}

		if isFolderMarkerKey(object.ObjectKey) {
			directoryName := folderArchiveDirectoryName(rootName, relativePath)
			if directoryName == "" {
				continue
			}

			if err := writeFolderArchiveDirectory(zipWriter, directories, directoryName); err != nil {
				_ = zipWriter.Close()
				return apperrors.Wrap(500, "folder_archive_failed", "failed to write folder archive", err)
			}
			continue
		}

		fileHeader := &zip.FileHeader{
			Name:   path.Join(rootName, relativePath),
			Method: zip.Deflate,
		}
		fileHeader.SetModTime(object.UpdatedAt)

		entryWriter, err := zipWriter.CreateHeader(fileHeader)
		if err != nil {
			_ = zipWriter.Close()
			return apperrors.Wrap(500, "folder_archive_failed", "failed to write folder archive", err)
		}

		reader, err := s.storage.Open(object.StoragePath)
		if err != nil {
			_ = zipWriter.Close()
			return apperrors.Wrap(500, "folder_archive_failed", "failed to open folder archive object", err)
		}

		_, copyErr := io.Copy(entryWriter, reader)
		closeErr := reader.Close()
		if copyErr != nil {
			_ = zipWriter.Close()
			return apperrors.Wrap(500, "folder_archive_failed", "failed to stream folder archive object", copyErr)
		}
		if closeErr != nil {
			_ = zipWriter.Close()
			return apperrors.Wrap(500, "folder_archive_failed", "failed to close folder archive object", closeErr)
		}
	}

	if err := zipWriter.Close(); err != nil {
		return apperrors.Wrap(500, "folder_archive_failed", "failed to finalize folder archive", err)
	}

	return nil
}

func writeFolderArchiveDirectory(
	zipWriter *zip.Writer,
	directories map[string]struct{},
	name string,
) error {
	if _, exists := directories[name]; exists {
		return nil
	}

	header := &zip.FileHeader{
		Name:   name,
		Method: zip.Store,
	}
	if _, err := zipWriter.CreateHeader(header); err != nil {
		return err
	}

	directories[name] = struct{}{}
	return nil
}

func folderArchiveRootName(folderPath string) string {
	return path.Base(strings.TrimSuffix(folderPath, "/"))
}

func folderArchiveFilename(rootName string) string {
	return SanitizeOriginalFilename(rootName + ".zip")
}

func folderArchiveDirectoryName(rootName string, relativePath string) string {
	if relativePath == folderMarkerFilename {
		return ""
	}

	directoryPath := strings.TrimSuffix(relativePath, folderMarkerFilename)
	directoryPath = strings.TrimSuffix(directoryPath, "/")
	if directoryPath == "" {
		return ""
	}

	return path.Join(rootName, directoryPath) + "/"
}
