package persistence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	. "github.com/Masterminds/squirrel"
	"github.com/deluan/rest"
	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/utils/slice"
	"github.com/pocketbase/dbx"
)

type mediaFileRepository struct {
	sqlRepository
	sqlRestful
}

type dbMediaFile struct {
	*model.MediaFile `structs:",flatten"`
	Tags             string `structs:"-" json:"-"`
	Participations   string `structs:"-" json:"-"`
}

func (m *dbMediaFile) PostScan() error {
	m.MediaFile.Participations = parseParticipations(m.Participations)
	if m.Tags == "" {
		return nil
	}
	tags, err := parseTags(m.Tags)
	if err != nil {
		return err
	}
	if len(tags) != 0 {
		m.MediaFile.Tags = tags
	}

	m.MediaFile.Genre, m.MediaFile.Genres = tags.ToGenres()
	return nil
}

func (m *dbMediaFile) PostMapArgs(args map[string]any) error {
	fullText := []string{m.Title, m.Album, m.Artist, m.AlbumArtist,
		m.SortTitle, m.SortAlbumName, m.SortArtistName, m.SortAlbumArtistName, m.DiscSubtitle}
	fullText = append(fullText, m.MediaFile.Participations.AllNames()...)
	args["full_text"] = formatFullText(fullText...)
	delete(args, "tags")
	delete(args, "participations")
	return nil
}

type dbMediaFiles []dbMediaFile

func (m *dbMediaFiles) toModels() model.MediaFiles {
	res := make(model.MediaFiles, len(*m))
	for i, mf := range *m {
		res[i] = *mf.MediaFile
	}
	return res
}

func NewMediaFileRepository(ctx context.Context, db dbx.Builder) model.MediaFileRepository {
	r := &mediaFileRepository{}
	r.ctx = ctx
	r.db = db
	r.tableName = "media_file"
	r.filterMappings = map[string]filterFunc{
		"id":       idFilter(r.tableName),
		"title":    fullTextFilter(r.tableName),
		"starred":  booleanFilter,
		"genre_id": tagIDFilter,
	}
	if conf.Server.PreferSortTags {
		r.sortMappings = map[string]string{
			"title":     "COALESCE(NULLIF(sort_title,''),title)",
			"artist":    "COALESCE(NULLIF(sort_artist_name,''),order_artist_name) asc, COALESCE(NULLIF(sort_album_name,''),order_album_name) asc, release_date asc, disc_number asc, track_number asc",
			"album":     "COALESCE(NULLIF(sort_album_name,''),order_album_name) asc, release_date asc, disc_number asc, track_number asc, COALESCE(NULLIF(sort_artist_name,''),order_artist_name) asc, COALESCE(NULLIF(sort_title,''),title) asc",
			"random":    r.seededRandomSort(),
			"createdAt": "media_file.created_at",
		}
	} else {
		r.sortMappings = map[string]string{
			"title":     "order_title",
			"artist":    "order_artist_name asc, order_album_name asc, release_date asc, disc_number asc, track_number asc",
			"album":     "order_album_name asc, release_date asc, disc_number asc, track_number asc, order_artist_name asc, title asc",
			"random":    r.seededRandomSort(),
			"createdAt": "media_file.created_at",
		}
	}
	return r
}

func (r *mediaFileRepository) CountAll(options ...model.QueryOptions) (int64, error) {
	sql := r.newSelectWithAnnotation("media_file.id")
	sql = r.withTags(sql)
	return r.count(sql, options...)
}

func (r *mediaFileRepository) Exists(id string) (bool, error) {
	return r.exists(Select().Where(Eq{"media_file.id": id}))
}

func (r *mediaFileRepository) Put(m *model.MediaFile) error {
	m.CreatedAt = time.Now()
	id, err := r.putByMatch(Eq{"path": m.Path, "library_id": m.LibraryID}, m.ID, &dbMediaFile{MediaFile: m})
	if err != nil {
		return err
	}
	m.ID = id

	err = r.updateParticipations(m.ID, m.Participations)
	if err != nil {
		return err
	}
	return r.updateTags(m.ID, m.Tags)
}

func (r *mediaFileRepository) selectMediaFile(options ...model.QueryOptions) SelectBuilder {
	sql := r.newSelectWithAnnotation("media_file.id", options...).Columns("media_file.*")
	sql = r.withBookmark(sql, "media_file.id")
	sql = r.withParticipations(sql)
	sql = r.withTags(sql).GroupBy(r.tableName + ".id")
	//if len(options) > 0 && options[0].Filters != nil {
	//	s, _, _ := options[0].Filters.ToSql()
	//	// If there's any reference of genre in the filter, joins with genre
	//	if strings.Contains(s, "genre") {
	//		sql = r.withGenres(sql)
	// FIXME Genres
	//		// If there's no filter on genre_id, group the results by media_file.id
	//		if !strings.Contains(s, "genre_id") {
	//			sql = sql.GroupBy("media_file.id")
	//		}
	//	}
	//}
	return sql
}

func (r *mediaFileRepository) Get(id string) (*model.MediaFile, error) {
	sel := r.selectMediaFile().Where(Eq{"media_file.id": id})
	var res dbMediaFiles
	if err := r.queryAll(sel, &res); err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, model.ErrNotFound
	}
	return res[0].MediaFile, nil
}

func (r *mediaFileRepository) GetAll(options ...model.QueryOptions) (model.MediaFiles, error) {
	r.resetSeededRandom(options)
	sq := r.selectMediaFile(options...)
	var res dbMediaFiles
	err := r.queryAll(sq, &res, options...)
	if err != nil {
		return nil, err
	}
	return res.toModels(), nil
}

func (r *mediaFileRepository) FindByPath(path string) (*model.MediaFile, error) {
	sel := r.newSelect().Columns("*").Where(Like{"path": path})
	var res dbMediaFiles
	if err := r.queryAll(sel, &res); err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, model.ErrNotFound
	}
	return res[0].MediaFile, nil
}

func cleanPath(path string) string {
	path = filepath.Clean(path)
	if !strings.HasSuffix(path, string(os.PathSeparator)) {
		path += string(os.PathSeparator)
	}
	return path
}

func pathStartsWith(path string) Eq {
	substr := fmt.Sprintf("substr(path, 1, %d)", utf8.RuneCountInString(path))
	return Eq{substr: path}
}

// FindAllByPath only return mediafiles that are direct children of requested path
func (r *mediaFileRepository) FindAllByPath(path string) (model.MediaFiles, error) {
	// Query by path based on https://stackoverflow.com/a/13911906/653632
	path = cleanPath(path)
	pathLen := utf8.RuneCountInString(path)
	sel0 := r.newSelect().Columns("media_file.*", fmt.Sprintf("substr(path, %d) AS item", pathLen+2)).
		Where(pathStartsWith(path))
	sel := r.newSelect().Columns("*", "item NOT GLOB '*"+string(os.PathSeparator)+"*' AS isLast").
		Where(Eq{"isLast": 1}).FromSelect(sel0, "sel0")

	res := dbMediaFiles{}
	err := r.queryAll(sel, &res)
	return res.toModels(), err
}

// FindPathsRecursively returns a list of all subfolders of basePath, recursively
func (r *mediaFileRepository) FindPathsRecursively(basePath string) ([]string, error) {
	path := cleanPath(basePath)
	// Query based on https://stackoverflow.com/a/38330814/653632
	sel := r.newSelect().Columns(fmt.Sprintf("distinct rtrim(path, replace(path, '%s', ''))", string(os.PathSeparator))).
		Where(pathStartsWith(path))
	var res []string
	err := r.queryAllSlice(sel, &res)
	return res, err
}

func (r *mediaFileRepository) deleteNotInPath(basePath string) error {
	path := cleanPath(basePath)
	sel := Delete(r.tableName).Where(NotEq(pathStartsWith(path)))
	c, err := r.executeSQL(sel)
	if err == nil {
		if c > 0 {
			log.Debug(r.ctx, "Deleted dangling tracks", "totalDeleted", c)
		}
	}
	return err
}

func (r *mediaFileRepository) Delete(id string) error {
	return r.delete(Eq{"id": id})
}

// DeleteByPath delete from the DB all mediafiles that are direct children of path
func (r *mediaFileRepository) DeleteByPath(basePath string) (int64, error) {
	path := cleanPath(basePath)
	pathLen := utf8.RuneCountInString(path)
	del := Delete(r.tableName).
		Where(And{pathStartsWith(path),
			Eq{fmt.Sprintf("substr(path, %d) glob '*%s*'", pathLen+2, string(os.PathSeparator)): 0}})
	log.Debug(r.ctx, "Deleting mediafiles by path", "path", path)
	return r.executeSQL(del)
}

func (r *mediaFileRepository) MarkMissing(missing bool, mfs ...model.MediaFile) error {
	ids := slice.Map(mfs, func(m model.MediaFile) string { return m.ID })
	return slice.RangeByChunks(ids, 200, func(chunk []string) error {
		upd := Update(r.tableName).
			Set("missing", missing).
			Set("updated_at", timeToSQL(time.Now())).
			Where(And{
				Eq{"id": chunk},
				Eq{"missing": !missing},
			})
		c, err := r.executeSQL(upd)
		if err != nil || c == 0 {
			log.Error(r.ctx, "Error setting mediafile missing flag", "ids", chunk, err)
			return err
		}
		log.Debug(r.ctx, "Marked missing mediafiles", "total", c, "ids", chunk)
		return nil
	})
}

func (r *mediaFileRepository) MarkMissingByFolder(missing bool, folderIDs ...string) error {
	return slice.RangeByChunks(folderIDs, 200, func(chunk []string) error {
		upd := Update(r.tableName).
			Set("missing", missing).
			Set("updated_at", timeToSQL(time.Now())).
			Where(And{
				Eq{"folder_id": chunk},
				Eq{"missing": !missing},
			})
		c, err := r.executeSQL(upd)
		if err != nil {
			log.Error(r.ctx, "Error setting mediafile missing flag", "folderIDs", chunk, err)
			return err
		}
		log.Debug(r.ctx, "Marked missing mediafiles from missing folders", "total", c, "folders", chunk)
		return nil
	})
}

// GetMissingAndMatching returns all mediafiles that are missing and their potential matches (comparing PIDs)
// that were added/updated after the last scan started
func (r *mediaFileRepository) GetMissingAndMatching(libId int, pagination ...model.QueryOptions) (model.MediaFiles, error) {
	subQ := r.newSelect().Columns("pid").
		Where(And{
			Eq{"media_file.missing": true},
			Eq{"library_id": libId},
		})
	subQText, subQArgs, err := subQ.PlaceholderFormat(Question).ToSql()
	if err != nil {
		return nil, err
	}
	sel := r.selectMediaFile(pagination...).
		Where("pid in ("+subQText+")", subQArgs...).
		Where(Or{
			Eq{"missing": true},
			ConcatExpr("media_file.created_at > library.last_scan_started_at"), // TODO Should it be `last_scan_at`?
		}).
		Join("library on media_file.library_id = library.id").
		OrderBy("pid")
	var res dbMediaFiles
	err = r.queryAll(sel, &res)
	if err != nil {
		return nil, err
	}
	return res.toModels(), nil
}

func (r *mediaFileRepository) removeNonAlbumArtistIds() error {
	upd := Update(r.tableName).Set("artist_id", "").Where(notExists("artist", ConcatExpr("id = artist_id")))
	log.Debug(r.ctx, "Removing non-album artist_ids")
	_, err := r.executeSQL(upd)
	return err
}

func (r *mediaFileRepository) Search(q string, offset int, size int) (model.MediaFiles, error) {
	results := dbMediaFiles{}
	err := r.doSearch(q, offset, size, &results, "title")
	if err != nil {
		return nil, err
	}
	return results.toModels(), err
}

func (r *mediaFileRepository) Count(options ...rest.QueryOptions) (int64, error) {
	return r.CountAll(r.parseRestOptions(options...))
}

func (r *mediaFileRepository) Read(id string) (interface{}, error) {
	return r.Get(id)
}

func (r *mediaFileRepository) ReadAll(options ...rest.QueryOptions) (interface{}, error) {
	return r.GetAll(r.parseRestOptions(options...))
}

func (r *mediaFileRepository) EntityName() string {
	return "mediafile"
}

func (r *mediaFileRepository) NewInstance() interface{} {
	return &model.MediaFile{}
}

var _ model.MediaFileRepository = (*mediaFileRepository)(nil)
var _ model.ResourceRepository = (*mediaFileRepository)(nil)
