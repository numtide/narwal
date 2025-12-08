WITH
    inventory_daily AS (
        SELECT
            toDate(last_modified_date) AS day,
            count() AS total_entries
        FROM file('/home/brian/Development/com/github/numtide/narwal/.data/inventory-mount/manifests/2025-12-05T01-00Z/*.parquet')
        WHERE key LIKE '%.narinfo'
        GROUP BY day
    ),
    orphans_daily AS (
        SELECT
            toDate(last_modified_date) AS day,
            count() AS orphan_entries
        FROM file('/home/brian/Development/com/github/numtide/narwal/.data/inventory-not-in-buildstepoutputs.csv', 'CSVWithNames')
        GROUP BY day
    )
SELECT
    i.day,
    i.total_entries,
    coalesce(o.orphan_entries, 0) AS orphan_entries,
    round(coalesce(o.orphan_entries, 0) * 100.0 / i.total_entries, 2) AS orphan_pct
FROM inventory_daily i
LEFT JOIN orphans_daily o ON i.day = o.day
ORDER BY i.day
INTO OUTFILE '/home/brian/Development/com/github/numtide/narwal/.data/daily-inventory-summary.csv'
FORMAT CSVWithNames
