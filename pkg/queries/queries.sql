-- name: ListTables :many
SELECT
    table_name
FROM information_schema.tables
WHERE
    table_schema = 'public'
ORDER BY
    table_name;

-- name: CreateUser :one
INSERT INTO users (username, fullname, emailaddress, password, type)
VALUES ($1, $2, $3, $4, 'hydra')
RETURNING *;

-- name: CreateProject :one
INSERT INTO projects (name, displayname, description, owner, enabled)
VALUES ($1, $2, $3, $4, 1)
RETURNING *;

-- name: CreateJobset :one
INSERT INTO jobsets (name, project, description, nixexprinput, nixexprpath, enabled, emailoverride)
VALUES ($1, $2, $3, $4, $5, 1, '')
RETURNING *;

-- name: CreateBuild :one
INSERT INTO builds (jobset_id, job, drvpath, system, finished, timestamp, buildstatus, priority, starttime, stoptime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: CreateBuildStep :exec
INSERT INTO buildsteps (build, stepnr, type, drvpath, status, machine, starttime, stoptime, busy)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0);

-- name: CreateBuildStepOutput :exec
INSERT INTO buildstepoutputs (build, stepnr, name, path)
VALUES ($1, $2, $3, $4);

-- Bulk insert queries using COPY protocol

-- name: CopyBuilds :copyfrom
INSERT INTO builds (jobset_id, job, drvpath, system, finished, timestamp, buildstatus, priority, starttime, stoptime)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: CopyBuildSteps :copyfrom
INSERT INTO buildsteps (build, stepnr, type, drvpath, status, machine, starttime, stoptime, busy)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: CopyBuildStepOutputs :copyfrom
INSERT INTO buildstepoutputs (build, stepnr, name, path)
VALUES ($1, $2, $3, $4);