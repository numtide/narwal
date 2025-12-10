-- +goose Up
-- +goose StatementBegin

create type object_type as enum('nar', 'narinfo', 'ls', 'log', 'debug');
create type compression_type as enum ('br', 'bz2', 'compress', 'grzip', 'gzip', 'lrzip', 'lz4', 'lzip', 'lzma', 'lzop', 'xz', 'zstd', 'none');

create table object
(
    hash bytea not null,
    object_type object_type not null,
    path varchar(256) not null,
    size bigint constraint positive_size check (size >= 0) not null,
    last_modified_at timestamp not null,

    primary key (object_type, hash, path)
) partition by list (object_type);

-- Create LIST partitions for each object_type
create table object_nar partition of object for values in ('nar');
create table object_narinfo partition of object for values in ('narinfo');
create table object_ls partition of object for values in ('ls');
create table object_debug partition of object for values in ('debug');
create table object_log partition of object for values in ('log');

create index idx_object_type on object(object_type);

create table nar_info
(
    hash bytea not null,
    url varchar(128) not null,
    store_path varchar(1024) not null,
    compression compression_type not null,

    file_hash varchar(128) not null,
    file_size bigint constraint positive_file_size check (file_size >= 0) not null,

    nar_hash varchar(128) not null,
    nar_size bigint constraint positive_nar_size check (nar_size >= 0) not null,

    deriver varchar(1024) not null,
    "references" bytea[] not null default '{}',
    "signatures" varchar(1024)[] not null default '{}',

    primary key (hash)
);

create table gc_root
(
    hash bytea primary key
        references nar_info(hash) on delete restrict,
    created_at timestamp not null
);

create table gc_plan
(
    id serial primary key,
    created_at timestamp not null,
    completed_at timestamp null
);

create function generate_gc_root_closure(plan_id integer) returns void AS
$$
declare
    table_name text;
    gc_root_record RECORD;
begin
    table_name := format('gc_plan_%s_closure', plan_id);
    execute format('create table if not exists %I (hash bytea primary key)', table_name);
    execute format('truncate table %I', table_name);
    execute format('create index if not exists idx_%I_hash on %I(hash)', table_name, table_name);

    for gc_root_record in select * from gc_root
    loop
        execute format('insert into %I (hash) values ($1) on conflict (hash) do nothing',
                      table_name) using gc_root_record.hash;

        execute format('
            with recursive reference_closure as (
                select hash, unnest("references") as refers_to
                from nar_info
                where hash = $1
                union
                select rc.hash, unnest(ni."references") as refers_to
                from nar_info ni
                inner join reference_closure rc on ni.hash = rc.refers_to
            )
            insert into %I (hash)
            (select distinct refers_to as hash from reference_closure)
            on conflict (hash) do nothing
        ', table_name) using gc_root_record.hash;
    end loop;
end;
$$ language plpgsql;

create table imported_manifest_file
(
    basename varchar(256) primary key,
    md5_checksum varchar(32) not null,
    size bigint constraint positive_size check (size >= 0) not null,
    imported_at timestamp not null
);

create index idx_imported_manifest_file_imported_at on imported_manifest_file(imported_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop function generate_gc_root_closure;

drop index idx_object_type;

drop table gc_plan;
drop table gc_root;
drop table nar_info;
drop table object;

drop type compression_type;
drop type object_type;

drop index idx_imported_manifest_file_imported_at;
drop table imported_manifest_file;

-- +goose StatementEnd
