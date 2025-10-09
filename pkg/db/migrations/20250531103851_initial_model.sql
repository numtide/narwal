-- +goose Up
-- +goose StatementBegin

create type object_type as enum('nar', 'narinfo', 'ls', 'log', 'debug');
create type compression_type as enum ('br', 'bz2', 'compress', 'grzip', 'gzip', 'lrzip', 'lz4', 'lzip', 'lzma', 'lzop', 'xz', 'zstd', 'none');

create table object
(
    hash varchar(52) not null,
    object_type object_type not null,
    compression_type compression_type not null,
    path varchar(128) not null,
    size bigint constraint positive_size check (size > 0) not null,
    created_at timestamp not null,
    last_accessed_at timestamp,

    primary key (hash, object_type, compression_type)
) partition by list (object_type);

-- Create LIST partitions for each object_type, then HASH sub-partition each
create table object_nar partition of object
    for values in ('nar')
    partition by hash (hash);

create table object_narinfo partition of object
    for values in ('narinfo')
    partition by hash (hash);

create table object_ls partition of object
    for values in ('ls')
    partition by hash (hash);

create table object_debug partition of object
    for values in ('debug')
    partition by hash (hash);

create table object_log partition of object
    for values in ('log')
    partition by hash (hash);

do $$
declare
    i integer;
begin
    for i in 1..128 loop
        -- Create 128 hash sub-partitions for nar using DO block
        execute format('create table object_nar_p%s partition of object_nar for values with (modulus 128, remainder %s)',
                      lpad(i::text, 3, '0'), i - 1);

        -- Create 128 hash sub-partitions for narinfo (same as nar, paired storage)
        execute format('create table object_narinfo_p%s partition of object_narinfo for values with (modulus 128, remainder %s)',
                       lpad(i::text, 3, '0'), i - 1);
    end loop;
end $$;


do $$
declare
    i integer;
begin
    for i in 1..64 loop
        -- Create 64 hash sub-partitions for ls
        execute format('create table object_ls_p%s partition of object_ls for values with (modulus 64, remainder %s)',
                      lpad(i::text, 2, '0'), i - 1);

        -- Create 64 hash sub-partitions for debug
        execute format('create table object_debug_p%s partition of object_debug for values with (modulus 64, remainder %s)',
                       lpad(i::text, 2, '0'), i - 1);
    end loop;
end $$;

-- Create 32 hash sub-partitions for log (lower volume)
do $$
declare
    i integer;
begin
    for i in 1..32 loop
        execute format('create table object_log_p%s partition of object_log for values with (modulus 32, remainder %s)',
                      lpad(i::text, 2, '0'), i - 1);
    end loop;
end $$;

create index idx_object_type on object(object_type);
create unique index idx_object_path on object(object_type, hash, path);

create table nar_info
(
    hash char(32) not null,
    url varchar(128) not null,
    store_path varchar(1024) not null,
    compression compression_type not null,

    file_hash varchar(128) not null,
    file_size bigint constraint positive_file_size check (file_size > 0) not null,

    nar_hash varchar(128) not null,
    nar_size bigint constraint positive_nar_size check (nar_size > 0) not null,

    deriver varchar(1024) not null,

    primary key (hash)
) partition by hash (hash);

-- Create 128 partitions for nar_info using DO block
do $$
declare
    i integer;
begin
    for i in 1..128 loop
        execute format('create table nar_info_p%s partition of nar_info for values with (modulus 128, remainder %s)',
                      lpad(i::text, 3, '0'), i - 1);
    end loop;
end $$;

create table nar_info_reference
(
    hash varchar(32) not null references nar_info on delete cascade,
    refers_to varchar(32) not null,
    primary key (hash, refers_to)
) partition by hash (hash);

-- Create 128 partitions for nar_info_reference using DO block
do $$
declare
    i integer;
begin
    for i in 1..128 loop
        execute format('create table nar_info_reference_p%s partition of nar_info_reference for values with (modulus 128, remainder %s)',
                      lpad(i::text, 3, '0'), i - 1);
    end loop;
end $$;

create index idx_nar_info_reference_refers_to on nar_info_reference(refers_to);

create table nar_info_signature
(
    hash varchar(32) not null references nar_info on delete cascade,
    name varchar(128) not null,
    data varchar(512) not null,
    primary key (hash, name)
) partition by hash (hash);

-- Create 128 partitions for nar_info_signature using DO block
do $$
declare
    i integer;
begin
    for i in 1..128 loop
        execute format('create table nar_info_signature_p%s partition of nar_info_signature for values with (modulus 128, remainder %s)',
                      lpad(i::text, 3, '0'), i - 1);
    end loop;
end $$;

create index idx_nar_info_signature_name on nar_info_signature(name);

create table gc_root
(
    hash varchar(32) primary key
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
    -- Create dynamic table name
    table_name := format('gc_plan_%s_closure', plan_id);

    -- Create the dynamic table if it doesn't exist
    execute format('create table if not exists %I (hash varchar(32) primary key)', table_name);

    -- Clear existing data from the table if it already exists
    execute format('truncate table %I', table_name);

    -- There will be duplicate entries so we create an index to make a distinct() query faster
    execute format('create index if not exists idx_%I_hash on %I(hash)', table_name, table_name);

    for gc_root_record in select * from gc_root
        loop
            -- Insert the GC root
            execute format('insert into %I (hash) values (%L) on conflict (hash) do nothing', table_name, gc_root_record.hash);

            -- For each hash in nar_info, apply the recursive query and insert results
            execute format('
            with recursive reference_closure as (
                select
                    hash,
                    refers_to
                from nar_info_reference
                where
                    hash = %L
                union
                select
                    rc.hash,
                    nir.refers_to
                from nar_info_reference nir
                inner join reference_closure rc on nir.hash = rc.refers_to
                where
                    nir.hash != nir.refers_to
            )
            insert into %I (hash)
            (select distinct refers_to as hash from reference_closure)
            on conflict (hash) do nothing
        ', gc_root_record.hash, table_name);

        end loop;

end;
$$ language plpgsql;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop function generate_gc_root_closure;

drop index idx_nar_info_signature_name;
drop index idx_nar_info_reference_refers_to;
drop index idx_object_type;
drop index idx_object_path;

drop table gc_plan;
drop table gc_root;
drop table nar_info_signature;
drop table nar_info_reference;
drop table nar_info;
drop table object;

drop type compression_type;
drop type object_type;

-- +goose StatementEnd
