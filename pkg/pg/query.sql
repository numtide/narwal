-- name: PutNar :exec
insert into nar_file (hash, bucket, path, size, created_at)
values ($1, $2, $3, $4, timezone('UTC', now())) on conflict
do nothing;

-- name: NarExists :one
select bucket, path, size
from nar_file
where hash = $1;

-- name: PutNarInfo :one
insert
into nar_info (hash, store_path, compression, file_hash, file_size, nar_hash, nar_size, deriver, bucket, path, size,
               created_at)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, timezone('UTC', now())) on conflict (hash)
do
update set hash = EXCLUDED.hash
    returning hash, (xmax = 0) as inserted;

-- name: InsertNarInfoReferences :exec
insert into nar_info_reference (hash, refers_to, created_at)
values ($1, $2, timezone('UTC', now()));

-- name: InsertNarInfoSignatures :exec
insert into nar_info_signature (hash, signature, created_at)
values ($1, $2, timezone('UTC', now()));

-- name: NarInfoExists :one
select bucket, path, size
from nar_info
where hash = $1;