package storage

import (
	"io"
	"os"
	"path/filepath"

	"github.com/mayswind/ezbookkeeping/pkg/core"
	"github.com/mayswind/ezbookkeeping/pkg/settings"
	"github.com/mayswind/ezbookkeeping/pkg/utils"
)

// LocalFileSystemObjectStorage represents local file system object storage
type LocalFileSystemObjectStorage struct {
	rootPath string
}

// NewLocalFileSystemObjectStorage returns a local file system object storage
func NewLocalFileSystemObjectStorage(config *settings.Config, pathPrefix string) (*LocalFileSystemObjectStorage, error) {
	storage := &LocalFileSystemObjectStorage{
		rootPath: filepath.Join(config.LocalFileSystemPath, pathPrefix),
	}

	if err := os.MkdirAll(storage.rootPath, os.ModePerm); err != nil {
		return nil, err
	}

	return storage, nil
}

// Exists returns whether the file exists
func (s *LocalFileSystemObjectStorage) Exists(ctx core.Context, path string) (bool, error) {
	return utils.IsExists(s.getFinalPath(path))
}

// Read returns the object instance according to specified the file path
func (s *LocalFileSystemObjectStorage) Read(ctx core.Context, path string) (ObjectInStorage, error) {
	return os.Open(s.getFinalPath(path))
}

// Save returns whether save the object instance successfully
func (s *LocalFileSystemObjectStorage) Save(ctx core.Context, path string, object ObjectInStorage) error {
	finalPath := s.getFinalPath(path)

	if err := os.MkdirAll(filepath.Dir(finalPath), os.ModePerm); err != nil {
		return err
	}

	// Save to a temporary file in the same directory first, then rename it to the final path.
	// This ensures the object is either fully written or does not appear at the target path at all.
	targetFile, err := os.CreateTemp(filepath.Dir(finalPath), ".tmp-*")

	if err != nil {
		return err
	}

	tempPath := targetFile.Name()
	completed := false

	defer func() {
		if !completed {
			targetFile.Close()
			_ = os.Remove(tempPath)
		}
	}()

	_, err = io.Copy(targetFile, object)

	if err != nil {
		return err
	}

	if err = targetFile.Close(); err != nil {
		return err
	}

	_ = os.Chmod(tempPath, 0644)

	if err = os.Rename(tempPath, finalPath); err != nil {
		return err
	}

	completed = true

	return nil
}

// Delete returns whether delete the object according to specified the file path successfully
func (s *LocalFileSystemObjectStorage) Delete(ctx core.Context, path string) error {
	err := os.Remove(s.getFinalPath(path))

	if os.IsNotExist(err) {
		return nil
	}

	return err
}

// Move moves the object from the source path to the destination path
func (s *LocalFileSystemObjectStorage) Move(ctx core.Context, srcPath string, dstPath string) error {
	finalSrcPath := s.getFinalPath(srcPath)
	finalDstPath := s.getFinalPath(dstPath)

	if _, err := os.Stat(finalSrcPath); os.IsNotExist(err) {
		// The source object does not exist. If the destination object already exists, treat it as moved successfully.
		if _, dstErr := os.Stat(finalDstPath); dstErr == nil {
			return nil
		}

		return os.ErrNotExist
	}

	if err := os.MkdirAll(filepath.Dir(finalDstPath), os.ModePerm); err != nil {
		return err
	}

	err := os.Rename(finalSrcPath, finalDstPath)

	if os.IsNotExist(err) {
		if _, dstErr := os.Stat(finalDstPath); dstErr == nil {
			return nil
		}
	}

	return err
}

// List returns all objects under the specified prefix path
func (s *LocalFileSystemObjectStorage) List(ctx core.Context, prefixPath string) ([]ObjectInStorageInfo, error) {
	finalPrefixPath := s.getFinalPath(prefixPath)

	if _, err := os.Stat(finalPrefixPath); os.IsNotExist(err) {
		return []ObjectInStorageInfo{}, nil
	} else if err != nil {
		return nil, err
	}

	objects := make([]ObjectInStorageInfo, 0)
	err := filepath.WalkDir(finalPrefixPath, func(currentPath string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}

			return err
		}

		if d.IsDir() {
			return nil
		}

		fileInfo, err := d.Info()

		if err != nil {
			return err
		}

		relativePath, err := filepath.Rel(s.rootPath, currentPath)

		if err != nil {
			return err
		}

		objects = append(objects, ObjectInStorageInfo{
			Path:         filepath.ToSlash(relativePath),
			Size:         fileInfo.Size(),
			LastModified: fileInfo.ModTime(),
		})

		return nil
	})

	if err != nil {
		return nil, err
	}

	return objects, nil
}

func (s *LocalFileSystemObjectStorage) getFinalPath(path string) string {
	return filepath.Join(s.rootPath, filepath.FromSlash(path))
}
