-- Stage 1: Extract hashes from buildstepoutputs CSV to a temporary table
CREATE TABLE buildstep_hashes ENGINE = MergeTree() ORDER BY hash AS
SELECT DISTINCT assumeNotNull(substring(path, 12, 32)) AS hash
FROM file('/home/brian/Development/com/github/numtide/narwal/.data/buildstepoutputs-2025-12-05-17:38:30Z.csv', 'CSVWithNames')
WHERE path IS NOT NULL AND length(path) >= 43;

-- Stage 2: Extract hashes from inventory parquet files to a temporary table
CREATE TABLE inventory_hashes ENGINE = MergeTree() ORDER BY hash AS
SELECT DISTINCT assumeNotNull(replaceOne(key, '.narinfo', '')) AS hash
FROM file('/home/brian/Development/com/github/numtide/narwal/.data/inventory-mount/manifests/2025-12-05T01-00Z/*.parquet')
WHERE key LIKE '%.narinfo';

-- Stage 3: Find inventory entries that ARE in buildstepoutputs and output to file
SELECT i.hash
FROM inventory_hashes i
INNER JOIN buildstep_hashes b ON i.hash = b.hash
INTO OUTFILE '/home/brian/Development/com/github/numtide/narwal/.data/inventory-in-buildstepoutputs.csv'
FORMAT CSVWithNames;
