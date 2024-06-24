package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(upAddFolderTable, downAddFolderTable)
}

func upAddFolderTable(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
create table if not exists folder(
	id varchar not null
		primary key,
	library_id integer not null
	    		references library (id)
	    		 	on delete cascade,
	path varchar default '' not null,
	name varchar default '' not null,
	missing boolean default false not null,
	updated_at datetime default current_timestamp not null,
	created_at datetime default current_timestamp not null,
	parent_id varchar default '' not null
);

alter table library
	add column last_scan_started_at datetime default '0000-00-00 00:00:00' not null;

alter table media_file 
    add column folder_id varchar default "" not null;
alter table media_file 
    add column pid varchar default id not null;
alter table media_file
	add column missing boolean default false not null;
alter table media_file
	add column birth_time datetime default current_timestamp not null;

create index if not exists media_file_folder_id_ix
 	on media_file (folder_id);
create index if not exists media_file_pid_ix
	on media_file (pid);
create index if not exists media_file_missing_ix
	on media_file (missing,updated_at);
create index if not exists media_file_birth_time_ix
	on media_file (birth_time);

-- FIXME Needs to process current media_file.paths, creating folders as needed

alter table album
	add column scanned_at datetime default '0000-00-00 00:00:00' not null;

create index if not exists album_scanned_at_ix
	on album (scanned_at);

create table if not exists tag(
  	id varchar not null primary key,
  	tag_name varchar default '' not null,
  	tag_value varchar default '' not null,
  	constraint tags_name_value_ux
		unique (tag_name, tag_value)
);

create table if not exists item_tags(
    item_id varchar not null,
    item_type varchar not null,
    tag_name varchar not null,
    tag_id varchar not null,
  	constraint item_tags_ux
    	unique (item_id, item_type, tag_id)
);

create index if not exists item_tag_name_ix on item_tags(item_id, tag_name)
`)

	return err
}

func downAddFolderTable(ctx context.Context, tx *sql.Tx) error {
	// This code is executed when the migration is rolled back.
	return nil
}
