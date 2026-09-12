package storage

import (
	"testing"

	"github.com/mayswind/ezbookkeeping/pkg/settings"
)

func TestWebDAVObjectStorageGetFinalDirectoryURLUsesRootPath(t *testing.T) {
	storage := &WebDAVObjectStorage{
		webDavConfig: &settings.WebDAVConfig{Url: "https://dav.example/dav"},
		rootPath:     "ezbookkeeping/transaction",
	}

	tests := map[string]string{
		"":        "https://dav.example/dav/ezbookkeeping/transaction/",
		"staging": "https://dav.example/dav/ezbookkeeping/transaction/staging/",
	}
	for path, expected := range tests {
		if actual := storage.getFinalDirectoryUrl(path); actual != expected {
			t.Fatalf("getFinalDirectoryUrl(%q) = %q, want %q", path, actual, expected)
		}
	}
}
