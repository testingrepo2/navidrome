//nolint:unused
package storagetest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"testing/fstest"
	"time"

	"github.com/navidrome/navidrome/core/storage"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model/metadata"
	"github.com/navidrome/navidrome/utils/random"
)

// FakeStorage is a fake storage that provides a FakeFS.
// It is used for testing purposes.
type FakeStorage struct{ fs *FakeFS }

// Register registers the FakeStorage for the "fake" scheme. To use it, set the model.Library's Path to "fake:///music".
// The storage registered will always return the same FakeFS instance.
func Register(fs *FakeFS) {
	storage.Register("fake", func(url url.URL) storage.Storage { return &FakeStorage{fs: fs} })
}

func (s FakeStorage) FS() (storage.MusicFS, error) {
	return s.fs, nil
}

// FakeFS is a fake filesystem that can be used for testing purposes.
// It implements the storage.MusicFS interface and keeps all files in memory, by using a fstest.MapFS internally.
type FakeFS struct {
	fstest.MapFS
}

func (ffs *FakeFS) SetFiles(files fstest.MapFS) {
	ffs.MapFS = files
	ffs.createDirTimestamps()
}

// createDirTimestamps loops through all entries and creat directories entries in the map with the
// latest ModTime from any children of that directory.
func (ffs *FakeFS) createDirTimestamps() bool {
	var changed bool
	for filePath, file := range ffs.MapFS {
		dir := path.Dir(filePath)
		dirFile, ok := ffs.MapFS[dir]
		if !ok {
			dirFile = &fstest.MapFile{Mode: fs.ModeDir}
			ffs.MapFS[dir] = dirFile
		}
		if dirFile.ModTime.IsZero() {
			dirFile.ModTime = file.ModTime
			changed = true
		}
	}
	if changed {
		// If we updated any directory, we need to re-run the loop to update the parent directories
		ffs.createDirTimestamps()
	}
	return changed
}

// RmGlob removes all files that match the glob pattern.
//func (ffs *FakeFS) RmGlob(glob string) {
//	matches, err := fs.Glob(ffs, glob)
//	if err != nil {
//		panic(err)
//	}
//	for _, f := range matches {
//		delete(ffs.MapFS, f)
//	}
//}

// Touch sets the modification time of a file.
func (ffs *FakeFS) Touch(filePath string, t ...time.Time) {
	if len(t) == 0 {
		t = append(t, time.Now())
	}
	file, ok := ffs.MapFS[filePath]
	if ok {
		file.ModTime = t[0]
	} else {
		ffs.MapFS[filePath] = &fstest.MapFile{ModTime: t[0]}
	}
	dir := path.Dir(filePath)
	dirFile, ok := ffs.MapFS[dir]
	if !ok {
		log.Fatal("Directory not found. Forgot to call SetFiles?", "file", filePath)
	}
	if dirFile.ModTime.Before(file.ModTime) {
		dirFile.ModTime = file.ModTime
	}
}

func ModTime(ts string) map[string]any   { return map[string]any{fakeFileInfoModTime: ts} }
func BirthTime(ts string) map[string]any { return map[string]any{fakeFileInfoBirthTime: ts} }

func (ffs *FakeFS) UpdateTags(filePath string, newTags map[string]any) {
	f, ok := ffs.MapFS[filePath]
	if !ok {
		panic(fmt.Errorf("file %s not found", filePath))
	}
	var tags map[string]any
	err := json.Unmarshal(f.Data, &tags)
	if err != nil {
		panic(err)
	}
	for k, v := range newTags {
		tags[k] = v
	}
	data, _ := json.Marshal(tags)
	f.Data = data
	ffs.Touch(filePath)
}

func Template(t map[string]any) func(...map[string]any) *fstest.MapFile {
	return func(tags ...map[string]any) *fstest.MapFile {
		return MP3(append([]map[string]any{t}, tags...)...)
	}
}

func Track(num int, title string) map[string]any {
	t := audioProperties("mp3", 320)
	t["title"] = title
	t["track"] = num
	return t
}

func MP3(tags ...map[string]any) *fstest.MapFile {
	ts := audioProperties("mp3", 320)
	if _, ok := ts[fakeFileInfoSize]; !ok {
		duration := ts["duration"].(int64)
		bitrate := ts["bitrate"].(int)
		ts[fakeFileInfoSize] = duration * int64(bitrate) / 8 * 1000
	}
	return File(append([]map[string]any{ts}, tags...)...)
}

func File(tags ...map[string]any) *fstest.MapFile {
	ts := map[string]any{}
	for _, t := range tags {
		for k, v := range t {
			ts[k] = v
		}
	}
	modTime := time.Now()
	if mt, ok := ts[fakeFileInfoModTime]; !ok {
		ts[fakeFileInfoModTime] = time.Now().Format(time.RFC3339)
	} else {
		modTime, _ = time.Parse(time.RFC3339, mt.(string))
	}
	if _, ok := ts[fakeFileInfoBirthTime]; !ok {
		ts[fakeFileInfoBirthTime] = time.Now().Format(time.RFC3339)
	}
	if _, ok := ts[fakeFileInfoMode]; !ok {
		ts[fakeFileInfoMode] = fs.ModePerm
	}
	data, _ := json.Marshal(ts)
	if _, ok := ts[fakeFileInfoSize]; !ok {
		ts[fakeFileInfoSize] = int64(len(data))
	}
	return &fstest.MapFile{Data: data, ModTime: modTime, Mode: ts[fakeFileInfoMode].(fs.FileMode)}
}

func audioProperties(suffix string, bitrate int) map[string]any {
	duration := random.Int64N(300) + 120
	return map[string]any{
		"suffix":     suffix,
		"bitrate":    bitrate,
		"duration":   duration,
		"samplerate": 44100,
		"bitdepth":   16,
		"channels":   2,
	}
}

func (ffs *FakeFS) ReadTags(paths ...string) (map[string]metadata.Info, error) {
	result := make(map[string]metadata.Info)
	for _, file := range paths {
		p, err := ffs.parseFile(file)
		if err != nil {
			return nil, err
		}
		result[file] = *p
	}
	return result, nil
}

func (ffs *FakeFS) parseFile(filePath string) (*metadata.Info, error) {
	contents, err := fs.ReadFile(ffs, filePath)
	if err != nil {
		return nil, err
	}
	data := map[string]any{}
	err = json.Unmarshal(contents, &data)
	if err != nil {
		return nil, err
	}
	p := metadata.Info{
		Tags:            map[string][]string{},
		AudioProperties: metadata.AudioProperties{},
		HasPicture:      data["has_picture"] == "true",
	}
	if d, ok := data["duration"].(float64); ok {
		p.AudioProperties.Duration = time.Duration(d) * time.Second
	}
	getInt := func(key string) int { v, _ := data[key].(float64); return int(v) }
	p.AudioProperties.BitRate = getInt("bitrate")
	p.AudioProperties.BitDepth = getInt("bitdepth")
	p.AudioProperties.SampleRate = getInt("samplerate")
	p.AudioProperties.Channels = getInt("channels")
	for k, v := range data {
		p.Tags[k] = []string{fmt.Sprintf("%v", v)}
	}
	file := ffs.MapFS[filePath]
	p.FileInfo = &fakeFileInfo{path: filePath, tags: data, file: file}
	return &p, nil
}

const (
	fakeFileInfoMode      = "_mode"
	fakeFileInfoSize      = "_size"
	fakeFileInfoModTime   = "_modtime"
	fakeFileInfoBirthTime = "_birthtime"
)

type fakeFileInfo struct {
	path string
	file *fstest.MapFile
	tags map[string]any
}

func (ffi *fakeFileInfo) Name() string         { return path.Base(ffi.path) }
func (ffi *fakeFileInfo) Size() int64          { v, _ := ffi.tags[fakeFileInfoSize].(float64); return int64(v) }
func (ffi *fakeFileInfo) Mode() fs.FileMode    { return ffi.file.Mode }
func (ffi *fakeFileInfo) IsDir() bool          { return false }
func (ffi *fakeFileInfo) Sys() any             { return nil }
func (ffi *fakeFileInfo) ModTime() time.Time   { return ffi.file.ModTime }
func (ffi *fakeFileInfo) BirthTime() time.Time { return ffi.parseTime(fakeFileInfoBirthTime) }
func (ffi *fakeFileInfo) parseTime(key string) time.Time {
	t, _ := time.Parse(time.RFC3339, ffi.tags[key].(string))
	return t
}
