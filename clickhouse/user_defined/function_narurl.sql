CREATE OR REPLACE FUNCTION narURL AS (file_hash, compression) ->
    concat(
        'nar/',
        nixbase32(file_hash),
        '.nar',
        multiIf(
            compression = '' OR compression = 'none', '',
            compression = 'bzip2', '.bz2',
            concat('.', compression)
        )
    );
