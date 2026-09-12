package storage

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mayswind/ezbookkeeping/pkg/core"
	"github.com/mayswind/ezbookkeeping/pkg/errs"
	"github.com/mayswind/ezbookkeeping/pkg/httpclient"
	"github.com/mayswind/ezbookkeeping/pkg/log"
	"github.com/mayswind/ezbookkeeping/pkg/settings"
)

// WebDAVObjectStorage represents WebDAV object storage
type WebDAVObjectStorage struct {
	httpClient   *http.Client
	webDavConfig *settings.WebDAVConfig
	rootPath     string
}

// webDavObjectEntry represents an entry returned by the WebDAV PROPFIND request
type webDavObjectEntry struct {
	path         string // relative path to the root path of the object storage
	isCollection bool
	size         int64
	lastModified string
}

// webDavMultiStatus represents the WebDAV multistatus xml element
type webDavMultiStatus struct {
	XMLName   xml.Name            `xml:"multistatus"`
	Responses []webDavXmlResponse `xml:"response"`
}

// webDavXmlResponse represents the WebDAV response xml element
type webDavXmlResponse struct {
	Href     string              `xml:"href"`
	Prop     *webDavXmlProp      `xml:"prop"`
	PropStat []webDavXmlPropStat `xml:"propstat"`
}

// webDavXmlPropStat represents the WebDAV propstat xml element
type webDavXmlPropStat struct {
	Prop webDavXmlProp `xml:"prop"`
}

// webDavXmlProp represents the WebDAV prop xml element
type webDavXmlProp struct {
	ResourceType  *webDavXmlResourceType `xml:"resourcetype"`
	ContentLength int64                  `xml:"getcontentlength"`
	LastModified  string                 `xml:"getlastmodified"`
}

// webDavXmlResourceType represents the WebDAV resourcetype xml element
type webDavXmlResourceType struct {
	Collection *struct{} `xml:"collection"`
}

// NewWebDAVObjectStorage returns a WebDAV object storage
func NewWebDAVObjectStorage(config *settings.Config, pathPrefix string) (*WebDAVObjectStorage, error) {
	webDavConfig := config.WebDAVConfig

	storage := &WebDAVObjectStorage{
		httpClient:   httpclient.NewHttpClient(webDavConfig.RequestTimeout, webDavConfig.Proxy, webDavConfig.SkipTLSVerify, core.GetOutgoingUserAgent(), false),
		webDavConfig: webDavConfig,
		rootPath:     webDavConfig.RootPath,
	}

	storage.rootPath = storage.getFinalPath(pathPrefix)
	storage.rootPath = strings.ReplaceAll(storage.rootPath, "\\", "/")

	ctx := core.NewNullContext()
	exists, err := storage.directoryExists(ctx, storage.rootPath)

	if err != nil {
		return nil, err
	}

	if !exists {
		err := storage.createAllDirectories(ctx, "", storage.rootPath)

		if err != nil {
			return nil, err
		}
	}

	return storage, nil
}

// Exists returns whether the file exists
func (s *WebDAVObjectStorage) Exists(ctx core.Context, path string) (bool, error) {
	req, err := http.NewRequest("HEAD", s.getFinalFileUrl(path), nil)

	if err != nil {
		return false, err
	}

	req.SetBasicAuth(s.webDavConfig.Username, s.webDavConfig.Password)
	resp, err := s.httpClient.Do(req)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.Exists] cannot check file exists, because %s", err.Error())
		return false, err
	}

	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusOK {
		return true, nil
	} else if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}

	log.Errorf(ctx, "[webdav_storage.Exists] cannot check file exists, http status code is %d", resp.StatusCode)
	return false, errs.ErrSystemError
}

// Read returns the object instance according to specified the file path
func (s *WebDAVObjectStorage) Read(ctx core.Context, path string) (ObjectInStorage, error) {
	req, err := http.NewRequest("GET", s.getFinalFileUrl(path), nil)

	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(s.webDavConfig.Username, s.webDavConfig.Password)
	resp, err := s.httpClient.Do(req)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.Read] cannot get file, because %s", err.Error())
		return nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.Read] cannot read response (http status code %d) body, because %s", resp.StatusCode, err.Error())
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		log.Errorf(ctx, "[webdav_storage.Read] cannot get file, http status code is %d, response is %s", resp.StatusCode, string(body))
		return nil, errs.ErrSystemError
	}

	return newByteSliceObject(body), nil
}

// Save returns whether save the object instance successfully
func (s *WebDAVObjectStorage) Save(ctx core.Context, path string, object ObjectInStorage) error {
	finalPath := s.getFinalPath(path)
	dir := strings.ReplaceAll(filepath.Dir(finalPath), "\\", "/")

	exists, err := s.directoryExists(ctx, dir)

	if err != nil {
		return err
	}

	if !exists {
		rootExists, err := s.directoryExists(ctx, s.rootPath)

		if err != nil {
			return err
		}

		if !rootExists {
			err := s.createAllDirectories(ctx, "", s.rootPath)

			if err != nil {
				return err
			}
		}

		err = s.createAllDirectories(ctx, s.rootPath, strings.ReplaceAll(filepath.Dir(path), "\\", "/"))

		if err != nil {
			return err
		}
	}

	data, err := io.ReadAll(object)

	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", s.getFinalFileUrl(path), bytes.NewReader(data))

	if err != nil {
		return err
	}

	req.SetBasicAuth(s.webDavConfig.Username, s.webDavConfig.Password)
	resp, err := s.httpClient.Do(req)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.Save] cannot save file, because %s", err.Error())
		return err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.Save] cannot read response (http status code %d) body, because %s", resp.StatusCode, err.Error())
		return err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		log.Errorf(ctx, "[webdav_storage.Save] cannot save file, http status code is %d, response is %s", resp.StatusCode, string(body))
		return errs.ErrSystemError
	}

	return nil
}

// Delete returns whether delete the object according to specified the file path successfully
func (s *WebDAVObjectStorage) Delete(ctx core.Context, path string) error {
	req, err := http.NewRequest("DELETE", s.getFinalFileUrl(path), nil)

	if err != nil {
		return err
	}

	req.SetBasicAuth(s.webDavConfig.Username, s.webDavConfig.Password)
	resp, err := s.httpClient.Do(req)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.Delete] cannot delete file, because %s", err.Error())
		return err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.Delete] cannot read response (http status code %d) body, because %s", resp.StatusCode, err.Error())
		return err
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusOK {
		log.Errorf(ctx, "[webdav_storage.Delete] cannot delete file, http status code is %d, response is %s", resp.StatusCode, string(body))
		return errs.ErrSystemError
	}

	return nil
}

// Move moves the object from the source path to the destination path
func (s *WebDAVObjectStorage) Move(ctx core.Context, srcPath string, dstPath string) error {
	dstExists, err := s.Exists(ctx, dstPath)

	if err != nil {
		return err
	}

	srcExists, err := s.Exists(ctx, srcPath)

	if err != nil {
		return err
	}

	if !srcExists {
		// The source object does not exist. If the destination object already exists, treat it as moved successfully.
		if dstExists {
			return nil
		}

		return os.ErrNotExist
	}

	// Make sure the parent directory of the destination path exists, because some WebDAV servers do not create it automatically
	if err := s.ensureParentDirectoryExists(ctx, dstPath); err != nil {
		return err
	}

	req, err := http.NewRequest("MOVE", s.getFinalFileUrl(srcPath), nil)

	if err != nil {
		return err
	}

	req.SetBasicAuth(s.webDavConfig.Username, s.webDavConfig.Password)
	req.Header.Set("Destination", s.getFinalFileUrl(dstPath))
	req.Header.Set("Overwrite", "T")
	resp, err := s.httpClient.Do(req)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.Move] cannot move file, because %s", err.Error())
		return s.moveByCopyAndDelete(ctx, srcPath, dstPath)
	}

	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)

	if readErr != nil {
		log.Errorf(ctx, "[webdav_storage.Move] cannot read response (http status code %d) body, because %s", resp.StatusCode, readErr.Error())
		return readErr
	}

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}

	if resp.StatusCode == http.StatusNotFound {
		if dstExistsAfter, dstCheckErr := s.Exists(ctx, dstPath); dstCheckErr == nil && dstExistsAfter {
			return nil
		}

		return os.ErrNotExist
	}

	log.Warnf(ctx, "[webdav_storage.Move] cannot move file, http status code is %d, response is %s, try to copy and delete", resp.StatusCode, string(body))
	return s.moveByCopyAndDelete(ctx, srcPath, dstPath)
}

// List returns all objects under the specified prefix path
func (s *WebDAVObjectStorage) List(ctx core.Context, prefixPath string) ([]ObjectInStorageInfo, error) {
	objects := make([]ObjectInStorageInfo, 0)
	visitedDirectories := make(map[string]bool)
	pendingDirectories := []string{prefixPath}

	for len(pendingDirectories) > 0 {
		currentDirectory := strings.Trim(pendingDirectories[0], "/")
		pendingDirectories = pendingDirectories[1:]

		if visitedDirectories[currentDirectory] {
			continue
		}

		visitedDirectories[currentDirectory] = true

		entries, directoryExists, err := s.listDirectoryEntries(ctx, currentDirectory)

		if err != nil {
			return nil, err
		}

		if !directoryExists {
			continue
		}

		for i := 0; i < len(entries); i++ {
			entry := entries[i]

			if entry.isCollection {
				if !visitedDirectories[entry.path] {
					pendingDirectories = append(pendingDirectories, entry.path)
				}
			} else if entry.path != "" {
				lastModified, _ := http.ParseTime(entry.lastModified)

				objects = append(objects, ObjectInStorageInfo{
					Path:         entry.path,
					Size:         entry.size,
					LastModified: lastModified,
				})
			}
		}
	}

	return objects, nil
}

func (s *WebDAVObjectStorage) moveByCopyAndDelete(ctx core.Context, srcPath string, dstPath string) error {
	srcObject, err := s.Read(ctx, srcPath)

	if err != nil {
		return err
	}

	defer srcObject.Close()

	if err := s.Save(ctx, dstPath, srcObject); err != nil {
		return err
	}

	return s.Delete(ctx, srcPath)
}

func (s *WebDAVObjectStorage) ensureParentDirectoryExists(ctx core.Context, filePath string) error {
	parentDirectory := strings.ReplaceAll(filepath.Dir(filePath), "\\", "/")

	if parentDirectory == "." || parentDirectory == "" || parentDirectory == "/" {
		return nil
	}

	exists, err := s.directoryExists(ctx, parentDirectory)

	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	rootExists, err := s.directoryExists(ctx, s.rootPath)

	if err != nil {
		return err
	}

	if !rootExists {
		if err := s.createAllDirectories(ctx, "", s.rootPath); err != nil {
			return err
		}
	}

	return s.createAllDirectories(ctx, s.rootPath, parentDirectory)
}

func (s *WebDAVObjectStorage) listDirectoryEntries(ctx core.Context, directoryPath string) ([]webDavObjectEntry, bool, error) {
	requestUrl := s.getFinalDirectoryUrl(directoryPath)
	requestBody := bytes.NewReader([]byte(`<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><resourcetype/><getcontentlength/><getlastmodified/></prop></propfind>`))

	req, err := http.NewRequest("PROPFIND", requestUrl, requestBody)

	if err != nil {
		return nil, false, err
	}

	req.SetBasicAuth(s.webDavConfig.Username, s.webDavConfig.Password)
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	resp, err := s.httpClient.Do(req)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.listDirectoryEntries] cannot list directory \"%s\", because %s", directoryPath, err.Error())
		return nil, false, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.listDirectoryEntries] cannot read response (http status code %d) body, because %s", resp.StatusCode, err.Error())
		return nil, false, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}

	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		log.Errorf(ctx, "[webdav_storage.listDirectoryEntries] cannot list directory \"%s\", http status code is %d, response is %s", directoryPath, resp.StatusCode, string(body))
		return nil, false, errs.ErrSystemError
	}

	multiStatus := &webDavMultiStatus{}

	if err := xml.Unmarshal(body, multiStatus); err != nil {
		log.Errorf(ctx, "[webdav_storage.listDirectoryEntries] cannot parse response body, because %s, response is %s", err.Error(), string(body))
		return nil, false, err
	}

	rootAbsolutePath := s.getRootAbsolutePath()
	directoryAbsolutePath := rootAbsolutePath

	if directoryPath != "" {
		directoryAbsolutePath = strings.TrimSuffix(directoryAbsolutePath, "/") + "/" + directoryPath
	}

	entries := make([]webDavObjectEntry, 0, len(multiStatus.Responses))

	for i := 0; i < len(multiStatus.Responses); i++ {
		response := multiStatus.Responses[i]
		entryAbsolutePath, err := s.getResponseAbsolutePath(requestUrl, response.Href)

		if err != nil {
			log.Warnf(ctx, "[webdav_storage.listDirectoryEntries] cannot parse href \"%s\", because %s", response.Href, err.Error())
			continue
		}

		isSelf := strings.TrimSuffix(entryAbsolutePath, "/") == strings.TrimSuffix(directoryAbsolutePath, "/")

		if isSelf {
			continue
		}

		prop := s.getResponseProp(response)

		if prop == nil {
			prop = &webDavXmlProp{}
		}

		isChild, relativePath, hrefEndsWithSlash := s.resolveEntryPath(rootAbsolutePath, entryAbsolutePath, directoryAbsolutePath)

		if !isChild {
			continue
		}

		// The resourcetype property is authoritative. If the server does not return it,
		// treat the entry as a collection when its href ends with "/"
		isCollection := hrefEndsWithSlash

		if prop.ResourceType != nil {
			isCollection = prop.ResourceType.Collection != nil
		}

		entries = append(entries, webDavObjectEntry{
			path:         relativePath,
			isCollection: isCollection,
			size:         prop.ContentLength,
			lastModified: prop.LastModified,
		})
	}

	return entries, true, nil
}

func (s *WebDAVObjectStorage) getResponseProp(response webDavXmlResponse) *webDavXmlProp {
	if response.Prop != nil {
		return response.Prop
	}

	for i := 0; i < len(response.PropStat); i++ {
		prop := &response.PropStat[i].Prop

		if prop.ResourceType != nil || prop.ContentLength != 0 || prop.LastModified != "" {
			return prop
		}
	}

	if len(response.PropStat) > 0 {
		return &response.PropStat[0].Prop
	}

	return nil
}

func (s *WebDAVObjectStorage) resolveEntryPath(rootAbsolutePath string, entryAbsolutePath string, directoryAbsolutePath string) (bool, string, bool) {
	directoryPrefix := strings.TrimSuffix(directoryAbsolutePath, "/") + "/"

	if !strings.HasPrefix(entryAbsolutePath, directoryPrefix) {
		return false, "", false
	}

	relativeToDirectory := strings.TrimPrefix(entryAbsolutePath, directoryPrefix)
	endsWithSlash := strings.HasSuffix(relativeToDirectory, "/")
	relativeToDirectory = strings.TrimSuffix(relativeToDirectory, "/")

	// The PROPFIND request uses Depth:1, so only the immediate children of the listed directory should be processed
	if relativeToDirectory == "" || strings.Contains(relativeToDirectory, "/") {
		return false, "", false
	}

	rootPrefix := strings.TrimSuffix(rootAbsolutePath, "/") + "/"
	relativePath := strings.TrimPrefix(strings.TrimSuffix(entryAbsolutePath, "/"), rootPrefix)

	if relativePath == "" {
		return false, "", false
	}

	return true, relativePath, endsWithSlash
}

func (s *WebDAVObjectStorage) getResponseAbsolutePath(requestUrl string, href string) (string, error) {
	baseUrl, err := url.Parse(requestUrl)

	if err != nil {
		return "", err
	}

	hrefUrl, err := url.Parse(href)

	if err != nil {
		return "", err
	}

	absoluteUrl := baseUrl.ResolveReference(hrefUrl)
	absolutePath, err := url.PathUnescape(absoluteUrl.Path)

	if err != nil {
		return "", err
	}

	return path.Clean("/"+absolutePath) + s.getPathTrailingSlash(absoluteUrl.Path), nil
}

func (s *WebDAVObjectStorage) getPathTrailingSlash(originalPath string) string {
	if strings.HasSuffix(originalPath, "/") {
		return "/"
	}

	return ""
}

func (s *WebDAVObjectStorage) getRootAbsolutePath() string {
	configUrl, err := url.Parse(s.webDavConfig.Url)

	if err != nil {
		return path.Clean("/" + s.rootPath)
	}

	rootPath := strings.ReplaceAll(s.rootPath, "\\", "/")
	return path.Clean("/" + path.Join(configUrl.Path, rootPath))
}

func (s *WebDAVObjectStorage) directoryExists(ctx core.Context, path string) (bool, error) {
	req, err := http.NewRequest("PROPFIND", s.getFinalDirectoryUrl(path), nil)

	if err != nil {
		return false, err
	}

	req.SetBasicAuth(s.webDavConfig.Username, s.webDavConfig.Password)
	req.Header.Set("Depth", "0")
	resp, err := s.httpClient.Do(req)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.directoryExists] cannot check directory exists, because %s", err.Error())
		return false, err
	}

	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusMultiStatus || resp.StatusCode == http.StatusOK {
		return true, nil
	} else if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}

	log.Errorf(ctx, "[webdav_storage.directoryExists] cannot check directory exists, http status code is %d", resp.StatusCode)
	return false, errs.ErrSystemError
}

func (s *WebDAVObjectStorage) createDirectory(ctx core.Context, path string) error {
	req, err := http.NewRequest("MKCOL", s.getFinalDirectoryUrl(path), nil)

	if err != nil {
		return err
	}

	req.SetBasicAuth(s.webDavConfig.Username, s.webDavConfig.Password)
	resp, err := s.httpClient.Do(req)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.createDirectory] cannot create directory, because %s", err.Error())
		return err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	if err != nil {
		log.Errorf(ctx, "[webdav_storage.createDirectory] cannot read response (http status code %d) body, because %s", resp.StatusCode, err.Error())
		return err
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMethodNotAllowed {
		log.Errorf(ctx, "[webdav_storage.createDirectory] cannot create directory, http status code is %d, response is %s", resp.StatusCode, string(body))
		return errs.ErrSystemError
	}

	return nil
}

func (s *WebDAVObjectStorage) createAllDirectories(ctx core.Context, currentPath string, path string) error {
	directories := strings.Split(path, "/")

	for _, dir := range directories {
		if len(dir) == 0 {
			continue
		}

		currentPath = currentPath + "/" + dir
		exists, err := s.directoryExists(ctx, currentPath)

		if err != nil {
			return err
		}

		if !exists {
			err = s.createDirectory(ctx, currentPath)

			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *WebDAVObjectStorage) getFinalFileUrl(filePath string) string {
	finalUrl := s.webDavConfig.Url

	if len(finalUrl) < 1 || finalUrl[len(finalUrl)-1] != '/' {
		finalUrl = finalUrl + "/"
	}

	finalPath := s.getFinalPath(filePath)

	if len(finalPath) > 0 && finalPath[0] == '/' {
		finalPath = finalPath[1:]
	}

	return finalUrl + finalPath
}

func (s *WebDAVObjectStorage) getFinalDirectoryUrl(dirPath string) string {
	finalUrl := s.webDavConfig.Url

	if len(finalUrl) < 1 || finalUrl[len(finalUrl)-1] != '/' {
		finalUrl = finalUrl + "/"
	}

	if len(dirPath) > 0 && dirPath[0] == '/' {
		dirPath = dirPath[1:]
	}

	if len(dirPath) > 0 && dirPath[len(dirPath)-1] != '/' {
		dirPath = dirPath + "/"
	}

	return finalUrl + dirPath
}

func (s *WebDAVObjectStorage) getFinalPath(path string) string {
	rootPath := s.rootPath

	if len(rootPath) < 1 || rootPath[len(rootPath)-1] != '/' {
		rootPath = rootPath + "/"
	}

	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}

	path = strings.ReplaceAll(path, "\\", "/")

	return rootPath + path
}
