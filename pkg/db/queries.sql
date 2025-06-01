-- name: HasNar :one
with update_accessed as (
    update nar_file
    set last_accessed_at = timezone('UTC', now())
)
select bucket, path, size
from nar_file as nf
where nf.hash = $1
  and nf.compression = $2;

-- name: PutNar :exec
insert into nar_file (hash, compression, bucket, path, size, created_at)
values ($1, $2, $3, $4, $5, timezone('UTC', now()))
on conflict(hash, compression) do update
    set bucket     = excluded.bucket,
        path       = excluded.path,
        size       = excluded.size,
        created_at = timezone('UTC', now());

-- name: HasNarInfo :one
with update_accessed as (
    update nar_info
        set last_accessed_at = timezone('UTC', now())
)
select bucket,
       path,
       size
from nar_info as nf
where nf.hash = $1;

-- name: PutNarInfo :exec
insert
into nar_info (hash, store_path, compression, file_hash, file_size, nar_hash, nar_size, deriver, bucket, path, size,
               created_at)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, timezone('UTC', now()))
on conflict (hash) do update
    set store_path  = excluded.store_path,
        compression = excluded.compression,
        file_hash   = excluded.file_hash,
        file_size   = excluded.file_size,
        nar_hash    = excluded.nar_hash,
        nar_size    = excluded.nar_size,
        deriver     = excluded.deriver,
        bucket      = excluded.bucket,
        path        = excluded.path,
        size        = excluded.size,
        created_at  = timezone('UTC', now());

-- name: DeleteNarInfoReferences :exec
delete from nar_info_reference
where hash = $1;

-- name: InsertNarInfoReferences :copyfrom
insert into nar_info_reference (hash, refers_to)
values ($1, $2);

-- name: DeleteNarInfoSignatures :exec
delete from nar_info_signature
where hash = $1;

-- name: InsertNarInfoSignatures :copyfrom
insert into nar_info_signature (hash, name, data)
values ($1, $2, $3);
