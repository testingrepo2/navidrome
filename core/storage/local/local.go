package local

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"time"

	"github.com/djherbis/times"
	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/core/storage"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model/tag"
)

// localStorage implements a Storage that reads the files from the local filesystem and uses registered extractors
// to extract the metadata and tags from the files.
type localStorage struct {
	u         url.URL
	extractor Extractor
}

func newLocalStorage(u url.URL) storage.Storage {
	newExtractor, ok := extractors[conf.Server.Scanner.Extractor]
	if !ok || newExtractor == nil {
		log.Fatal("Extractor not found: %s", conf.Server.Scanner.Extractor)
	}
	return localStorage{u: u, extractor: newExtractor(os.DirFS(u.Path), u.Path)}
}

func (s localStorage) FS() (storage.MusicFS, error) {
	path := s.u.Path
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("%w: %s", err, path)
	}
	return localFS{FS: os.DirFS(path), extractor: s.extractor}, nil
}

type localFS struct {
	fs.FS
	extractor Extractor
}

func (lfs localFS) ReadTags(path ...string) (map[string]tag.Properties, error) {
	res, err := lfs.extractor.Parse(path...)
	if err != nil {
		return nil, err
	}
	for path, v := range res {
		if v.FileInfo == nil {
			info, err := fs.Stat(lfs, path)
			if err != nil {
				return nil, err
			}
			v.FileInfo = localFileInfo{info}
			res[path] = v
		}
	}
	return res, nil
}

// localFileInfo is a wrapper around fs.FileInfo that adds a BirthTime method, to make it compatible
// with tag.FileInfo
type localFileInfo struct {
	fs.FileInfo
}

func (lfi localFileInfo) BirthTime() time.Time {
	if ts := times.Get(lfi.FileInfo); ts.HasBirthTime() {
		return ts.BirthTime()
	}

	return time.Time{}
}

func init() {
	storage.Register(storage.LocalSchemaID, newLocalStorage)
}
