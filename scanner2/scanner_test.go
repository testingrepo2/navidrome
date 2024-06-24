package scanner2_test

import (
	"context"
	"testing/fstest"

	"github.com/Masterminds/squirrel"
	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/core/storage/storagetest"
	"github.com/navidrome/navidrome/db"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/persistence"
	"github.com/navidrome/navidrome/scanner"
	"github.com/navidrome/navidrome/scanner2"
	"github.com/navidrome/navidrome/utils/slice"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Easy aliases for the storagetest package
type _t = map[string]any

var template = storagetest.Template
var track = storagetest.Track

var _ = Describe("Scanner", func() {
	var ctx context.Context
	var lib model.Library
	var ds model.DataStore
	var s scanner.Scanner

	createFS := func(files fstest.MapFS) storagetest.FakeFS {
		fs := storagetest.FakeFS{}
		fs.SetFiles(files)
		storagetest.Register(&fs)
		return fs
	}

	BeforeEach(func() {
		ctx = context.Background()

		//log.SetLevel(log.LevelTrace)
		//os.Remove("./test-123.db")
		//conf.Server.DbPath = "./test-123.db"
		//dbpath := utils.TempFileName("scanner-test", ".db")
		//conf.Server.DbPath = dbpath + "?cache=shared&_foreign_keys=on"
		conf.Server.DbPath = "file::memory:?cache=shared&_foreign_keys=on"
		db.Init()
		ds = persistence.New(db.Db())
		s = scanner2.GetInstance(ctx, ds)

		lib = model.Library{ID: 1, Name: "Fake Library", Path: "fake:///music"}
		Expect(ds.Library(ctx).Put(&lib)).To(Succeed())
	})

	AfterEach(func() {
		_, err := db.Db().WriteDB().ExecContext(ctx, `
			PRAGMA writable_schema = 1;
			DELETE FROM sqlite_master;
			PRAGMA writable_schema = 0;
			VACUUM;
			PRAGMA integrity_check;
		`)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Scanner", func() {
		Context("Simple library, 'artis/album/track - title.mp3'", func() {
			var help, revolver func(...map[string]any) *fstest.MapFile
			var fsys storagetest.FakeFS
			BeforeEach(func() {
				revolver = template(_t{"albumartist": "The Beatles", "album": "Revolver", "year": 1966})
				help = template(_t{"albumartist": "The Beatles", "album": "Help!", "year": 1965})
				fsys = createFS(fstest.MapFS{
					"The Beatles/Revolver/01 - Taxman.mp3":                         revolver(track(1, "Taxman")),
					"The Beatles/Revolver/02 - Eleanor Rigby.mp3":                  revolver(track(2, "Eleanor Rigby")),
					"The Beatles/Revolver/03 - I'm Only Sleeping.mp3":              revolver(track(3, "I'm Only Sleeping")),
					"The Beatles/Revolver/04 - Love You To.mp3":                    revolver(track(4, "Love You To")),
					"The Beatles/Help!/01 - Help!.mp3":                             help(track(1, "Help!")),
					"The Beatles/Help!/02 - The Night Before.mp3":                  help(track(2, "The Night Before")),
					"The Beatles/Help!/03 - You've Got to Hide Your Love Away.mp3": help(track(3, "You've Got to Hide Your Love Away")),
				})
			})
			When("it is the first scan", func() {
				It("should import all folders", func() {
					Expect(s.RescanAll(ctx, true)).To(Succeed())

					folders, _ := ds.Folder(ctx).GetAll(lib)
					paths := slice.Map(folders, func(f model.Folder) string { return f.Name })
					Expect(paths).To(SatisfyAll(
						HaveLen(4),
						ContainElements(".", "The Beatles", "Revolver", "Help!"),
					))
				})
				It("should import all mediafiles", func() {
					Expect(s.RescanAll(ctx, true)).To(Succeed())

					mfs, _ := ds.MediaFile(ctx).GetAll()
					paths := slice.Map(mfs, func(f model.MediaFile) string { return f.Title })
					Expect(paths).To(SatisfyAll(
						HaveLen(7),
						ContainElements(
							"Taxman", "Eleanor Rigby", "I'm Only Sleeping", "Love You To",
							"Help!", "The Night Before", "You've Got to Hide Your Love Away",
						),
					))
				})
				It("should import all albums", func() {
					Expect(s.RescanAll(ctx, true)).To(Succeed())

					albums, _ := ds.Album(ctx).GetAll(model.QueryOptions{Sort: "name"})
					Expect(albums).To(HaveLen(2))
					Expect(albums[0]).To(SatisfyAll(
						HaveField("Name", Equal("Help!")),
						HaveField("SongCount", Equal(3)),
					))
					Expect(albums[1]).To(SatisfyAll(
						HaveField("Name", Equal("Revolver")),
						HaveField("SongCount", Equal(4)),
					))
				})
			})
			XWhen("a file was changed", func() {
				It("should update the media_file", func() {
					Expect(s.RescanAll(ctx, true)).To(Succeed())

					mf, err := ds.MediaFile(ctx).GetAll(model.QueryOptions{Filters: squirrel.Eq{"title": "Help!"}})
					Expect(err).ToNot(HaveOccurred())
					Expect(mf[0].Tags).ToNot(HaveKey("catalognumber"))

					fsys.UpdateTags("The Beatles/Help!/01 - Help!.mp3", _t{"catalognumber": "123"})
					Expect(s.RescanAll(ctx, true)).To(Succeed())

					mf, err = ds.MediaFile(ctx).GetAll(model.QueryOptions{Filters: squirrel.Eq{"title": "Help!"}})
					Expect(err).ToNot(HaveOccurred())
					Expect(mf[0].Tags).To(HaveKeyWithValue("catalognumber", []string{"123"}))
				})

				It("should update the album", func() {
					Expect(s.RescanAll(ctx, true)).To(Succeed())

					albums, err := ds.Album(ctx).GetAll(model.QueryOptions{Filters: squirrel.Eq{"name": "Help!"}})
					Expect(err).ToNot(HaveOccurred())
					Expect(albums[0].CatalogNum).To(BeEmpty())
					Expect(albums[0].SongCount).To(Equal(3))

					fsys.UpdateTags("The Beatles/Help!/01 - Help!.mp3", _t{"catalognumber": "123"})
					Expect(s.RescanAll(ctx, false)).To(Succeed())

					albums, err = ds.Album(ctx).GetAll(model.QueryOptions{Filters: squirrel.Eq{"name": "Help!"}})
					Expect(err).ToNot(HaveOccurred())
					Expect(albums[0].CatalogNum).To(Equal("123"))
					Expect(albums[0].SongCount).To(Equal(3))
				})
			})
		})

		Context("Same album in two different folders", func() {
			BeforeEach(func() {
				revolver := template(_t{"albumartist": "The Beatles", "album": "Revolver", "year": 1966})
				createFS(fstest.MapFS{
					"The Beatles/Revolver/01 - Taxman.mp3":         revolver(track(1, "Taxman")),
					"The Beatles/Revolver2/02 - Eleanor Rigby.mp3": revolver(track(2, "Eleanor Rigby")),
				})
			})

			It("should import as one album", func() {
				Expect(s.RescanAll(ctx, true)).To(Succeed())

				albums, err := ds.Album(ctx).GetAll()
				Expect(err).ToNot(HaveOccurred())
				Expect(albums).To(HaveLen(1))

				mfs, err := ds.MediaFile(ctx).GetAll()
				Expect(err).ToNot(HaveOccurred())
				Expect(mfs).To(HaveLen(2))
				for _, mf := range mfs {
					Expect(mf.AlbumID).To(Equal(albums[0].ID))
				}
			})
		})

		Context("Same album, different release dates", func() {
			BeforeEach(func() {
				conf.Server.Scanner.GroupAlbumReleases = false
				help := template(_t{"albumartist": "The Beatles", "album": "Help!", "date": 1965})
				help2 := template(_t{"albumartist": "The Beatles", "album": "Help!", "date": 2000})
				createFS(fstest.MapFS{
					"The Beatles/Help!/01 - Help!.mp3":            help(track(1, "Help!")),
					"The Beatles/Help! (remaster)/01 - Help!.mp3": help2(track(1, "Help!")),
				})
			})

			It("should import as two distinct albums", func() {
				Expect(s.RescanAll(ctx, true)).To(Succeed())

				albums, err := ds.Album(ctx).GetAll(model.QueryOptions{Sort: "date"})
				Expect(err).ToNot(HaveOccurred())
				Expect(albums).To(HaveLen(2))
				Expect(albums[0]).To(SatisfyAll(
					HaveField("Name", Equal("Help!")),
					HaveField("Date", Equal("1965")),
				))
				Expect(albums[1]).To(SatisfyAll(
					HaveField("Name", Equal("Help!")),
					HaveField("Date", Equal("2000")),
				))
			})
		})
	})
})
