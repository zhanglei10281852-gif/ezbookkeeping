package storage

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/mayswind/ezbookkeeping/pkg/core"
	"github.com/mayswind/ezbookkeeping/pkg/settings"
)

// MinIOObjectStorage represents MinIO object storage
type MinIOObjectStorage struct {
	minIOClient *minio.Client
	minIOConfig *settings.MinIOConfig
	rootPath    string
}

// NewMinIOObjectStorage returns a MinIO object storage
func NewMinIOObjectStorage(config *settings.Config, pathPrefix string) (*MinIOObjectStorage, error) {
	minIOConfig := config.MinIOConfig

	minIOClient, err := minio.New(minIOConfig.Endpoint, &minio.Options{
		Region:    minIOConfig.Location,
		Creds:     credentials.NewStaticV4(minIOConfig.AccessKeyID, minIOConfig.SecretAccessKey, ""),
		Secure:    minIOConfig.UseSSL,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: minIOConfig.SkipTLSVerify}},
	})

	if err != nil {
		return nil, err
	}

	storage := &MinIOObjectStorage{
		minIOClient: minIOClient,
		minIOConfig: minIOConfig,
		rootPath:    minIOConfig.RootPath,
	}

	storage.rootPath = storage.getFinalPath(pathPrefix)
	storage.rootPath = strings.ReplaceAll(storage.rootPath, "\\", "/")

	ctx := context.Background()
	exists, err := minIOClient.BucketExists(ctx, minIOConfig.Bucket)

	if err != nil {
		return nil, err
	}

	if !exists {
		err := minIOClient.MakeBucket(ctx, minIOConfig.Bucket, minio.MakeBucketOptions{
			Region: minIOConfig.Location,
		})

		if err != nil {
			return nil, err
		}
	}

	return storage, nil
}

// Exists returns whether the file exists
func (s *MinIOObjectStorage) Exists(ctx core.Context, path string) (bool, error) {
	objectInfo, err := s.minIOClient.StatObject(ctx, s.minIOConfig.Bucket, s.getFinalPath(path), minio.StatObjectOptions{})

	if err != nil {
		if isMinIOObjectNotExists(err) {
			return false, nil
		}

		return false, err
	}

	if objectInfo.IsDeleteMarker {
		return false, nil
	}

	return true, nil
}

// Read returns the object instance according to specified the file path
func (s *MinIOObjectStorage) Read(ctx core.Context, path string) (ObjectInStorage, error) {
	return s.minIOClient.GetObject(ctx, s.minIOConfig.Bucket, s.getFinalPath(path), minio.GetObjectOptions{})
}

// Save returns whether save the object instance successfully
func (s *MinIOObjectStorage) Save(ctx core.Context, path string, object ObjectInStorage) error {
	_, err := s.minIOClient.PutObject(ctx, s.minIOConfig.Bucket, s.getFinalPath(path), object, -1, minio.PutObjectOptions{})

	return err
}

// Delete returns whether delete the object according to specified the file path successfully
func (s *MinIOObjectStorage) Delete(ctx core.Context, path string) error {
	err := s.minIOClient.RemoveObject(ctx, s.minIOConfig.Bucket, s.getFinalPath(path), minio.RemoveObjectOptions{})

	if err != nil && isMinIOObjectNotExists(err) {
		return nil
	}

	return err
}

// Move moves the object from the source path to the destination path
func (s *MinIOObjectStorage) Move(ctx core.Context, srcPath string, dstPath string) error {
	finalSrcPath := s.getFinalPath(srcPath)
	finalDstPath := s.getFinalPath(dstPath)

	// If the destination object already exists, the move has been completed. Remove the source object idempotently.
	if _, err := s.minIOClient.StatObject(ctx, s.minIOConfig.Bucket, finalDstPath, minio.StatObjectOptions{}); err == nil {
		err := s.minIOClient.RemoveObject(ctx, s.minIOConfig.Bucket, finalSrcPath, minio.RemoveObjectOptions{})

		if err != nil && !isMinIOObjectNotExists(err) {
			return err
		}

		return nil
	}

	if _, err := s.minIOClient.StatObject(ctx, s.minIOConfig.Bucket, finalSrcPath, minio.StatObjectOptions{}); err != nil {
		if isMinIOObjectNotExists(err) {
			return os.ErrNotExist
		}

		return err
	}

	_, err := s.minIOClient.CopyObject(ctx,
		minio.CopyDestOptions{
			Bucket: s.minIOConfig.Bucket,
			Object: finalDstPath,
		},
		minio.CopySrcOptions{
			Bucket: s.minIOConfig.Bucket,
			Object: finalSrcPath,
		})

	if err != nil {
		if isMinIOObjectNotExists(err) {
			return os.ErrNotExist
		}

		return err
	}

	err = s.minIOClient.RemoveObject(ctx, s.minIOConfig.Bucket, finalSrcPath, minio.RemoveObjectOptions{})

	if err != nil && !isMinIOObjectNotExists(err) {
		return err
	}

	return nil
}

// List returns all objects under the specified prefix path
func (s *MinIOObjectStorage) List(ctx core.Context, prefixPath string) ([]ObjectInStorageInfo, error) {
	listPrefix := s.getFinalPath(prefixPath)

	if !strings.HasSuffix(listPrefix, "/") {
		listPrefix += "/"
	}

	// The root path prefix always ends with "/" and represents the root of this object storage
	rootPrefix := s.getFinalPath("")
	relativePrefix := strings.TrimPrefix(listPrefix, rootPrefix)

	objects := make([]ObjectInStorageInfo, 0)

	for object := range s.minIOClient.ListObjects(ctx, s.minIOConfig.Bucket, minio.ListObjectsOptions{
		Prefix:    listPrefix,
		Recursive: true,
	}) {
		if object.Err != nil {
			return nil, object.Err
		}

		if strings.HasSuffix(object.Key, "/") {
			continue
		}

		keyRelativePath := strings.TrimPrefix(object.Key, listPrefix)

		if keyRelativePath == object.Key || keyRelativePath == "" {
			continue
		}

		objects = append(objects, ObjectInStorageInfo{
			Path:         relativePrefix + keyRelativePath,
			Size:         object.Size,
			LastModified: object.LastModified,
		})
	}

	return objects, nil
}

func isMinIOObjectNotExists(err error) bool {
	errorResponse := minio.ToErrorResponse(err)
	return errorResponse.Code == "NoSuchKey" || errorResponse.Code == "NoSuchVersion" || errorResponse.StatusCode == http.StatusNotFound
}

func (s *MinIOObjectStorage) getFinalPath(path string) string {
	rootPath := s.rootPath

	if len(rootPath) > 0 && rootPath[len(rootPath)-1] != '/' {
		rootPath = rootPath + "/"
	}

	if len(rootPath) > 0 && rootPath[0] == '/' {
		rootPath = rootPath[1:]
	}

	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}

	path = strings.ReplaceAll(path, "\\", "/")

	return rootPath + path
}
