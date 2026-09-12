package storage

import (
	"time"

	"github.com/mayswind/ezbookkeeping/pkg/core"
)

// ObjectInStorageInfo represents the meta information of an object in the object storage
type ObjectInStorageInfo struct {
	Path         string // relative path to the root path of the object storage
	Size         int64
	LastModified time.Time
}

// ObjectStorage represents an object storage to store file object
type ObjectStorage interface {
	Exists(ctx core.Context, path string) (bool, error)
	Read(ctx core.Context, path string) (ObjectInStorage, error)
	Save(ctx core.Context, path string, object ObjectInStorage) error
	Delete(ctx core.Context, path string) error
	Move(ctx core.Context, srcPath string, dstPath string) error
	List(ctx core.Context, prefixPath string) ([]ObjectInStorageInfo, error)
}
