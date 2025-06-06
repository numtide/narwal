-- name: HasObject :one
with update_accessed as (
    update object
    set last_accessed_at = timezone('UTC', now())
    where path = $1
)
select object_type, compression_type, size
from object as o
where o.path = $1;

-- name: GetObjectByHash :one
select object_type, compression_type, size
from object as o
where o.hash = $1;


-- name: PutObject :exec
insert into object (hash, object_type, compression_type, path, size, created_at)
values ($1, $2, $3, $4, $5, timezone('UTC', now()))
on conflict(path) do update
    set size       = excluded.size,
        created_at = timezone('UTC', now());

-- name: PutNarInfo :exec
insert
into nar_info (hash, url, store_path, compression, file_hash, file_size, nar_hash, nar_size, deriver)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
on conflict (hash) do update
    set url = excluded.url,
        store_path  = excluded.store_path ,
        compression = excluded.compression,
        file_hash   = excluded.file_hash,
        file_size   = excluded.file_size,
        nar_hash    = excluded.nar_hash,
        nar_size    = excluded.nar_size,
        deriver     = excluded.deriver;

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

-- name: PutGCRoot :exec
insert into gc_root (hash, created_at)
values ($1, timezone('UTC', now()))
on conflict(hash) do nothing;

-- name: DeleteGCRoot :one
with deleted as (
    delete from gc_root where hash = $1 returning *
) select count(*) from deleted;

-- name: ListGCRoots :many
select hash from gc_root;

-- name: InsertGCPlan :one
insert into gc_plan (created_at)
values (timezone('UTC', now()))
returning id;

-- name: ListGCPlans :many
select * from gc_plan;

-- name: GetGCPlan :one
select * from gc_plan where id = $1;

-- name: DeleteGCPlan :one
with deleted as (
    delete from gc_plan where id = $1 returning *
) select count(*) from deleted;

-- name: SetGCPlanAsCompleted :exec
update gc_plan set completed_at = timezone('UTC', now()) where id = $1 and completed_at is null;