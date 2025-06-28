use parquet::file::properties::WriterProperties;
use parquet::file::writer::SerializedFileWriter;
use parquet::schema::parser::parse_message_type;
use std::fs::File;
use std::path::Path;
use std::sync::Arc;

/// S3 Inventory schema as used by narwal
const S3_INVENTORY_SCHEMA: &str = "
message s3_inventory {
    REQUIRED BYTE_ARRAY bucket (UTF8);
    REQUIRED BYTE_ARRAY key (UTF8);
    OPTIONAL INT64 size;
    OPTIONAL INT64 last_modified_date;
    REQUIRED BYTE_ARRAY e_tag (UTF8);
    REQUIRED BYTE_ARRAY storage_class (UTF8);
}
";

/// Create a test parquet file with S3 inventory records
pub fn create_test_parquet_file(
    path: &Path,
    narinfo_hashes: &[&str],
) -> Result<(), Box<dyn std::error::Error>> {
    let schema = Arc::new(parse_message_type(S3_INVENTORY_SCHEMA)?);
    let file = File::create(path)?;
    let writer_props = WriterProperties::builder().build();
    let mut writer = SerializedFileWriter::new(file, schema, Arc::new(writer_props))?;

    let mut row_group_writer = writer.next_row_group()?;

    // Write bucket column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let bucket_values: Vec<_> = narinfo_hashes
            .iter()
            .map(|_| parquet::data_type::ByteArray::from("nix-cache"))
            .collect();
        col_writer
            .typed::<parquet::data_type::ByteArrayType>()
            .write_batch(&bucket_values, None, None)?;
        col_writer.close()?;
    }

    // Write key column (nar/<hash>.narinfo)
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let key_values: Vec<_> = narinfo_hashes
            .iter()
            .map(|hash| {
                parquet::data_type::ByteArray::from(format!("nar/{}.narinfo", hash).as_str())
            })
            .collect();
        col_writer
            .typed::<parquet::data_type::ByteArrayType>()
            .write_batch(&key_values, None, None)?;
        col_writer.close()?;
    }

    // Write size column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let size_values: Vec<i64> = narinfo_hashes
            .iter()
            .enumerate()
            .map(|(i, _)| 1000 + i as i64 * 100)
            .collect();
        let def_levels: Vec<i16> = vec![1; narinfo_hashes.len()]; // All values are defined
        col_writer
            .typed::<parquet::data_type::Int64Type>()
            .write_batch(&size_values, Some(&def_levels), None)?;
        col_writer.close()?;
    }

    // Write last_modified_date column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let date_values: Vec<i64> = narinfo_hashes.iter().map(|_| 1700000000000).collect();
        let def_levels: Vec<i16> = vec![1; narinfo_hashes.len()]; // All values are defined
        col_writer
            .typed::<parquet::data_type::Int64Type>()
            .write_batch(&date_values, Some(&def_levels), None)?;
        col_writer.close()?;
    }

    // Write e_tag column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let etag_values: Vec<_> = narinfo_hashes
            .iter()
            .map(|_| parquet::data_type::ByteArray::from("\"abc123\""))
            .collect();
        col_writer
            .typed::<parquet::data_type::ByteArrayType>()
            .write_batch(&etag_values, None, None)?;
        col_writer.close()?;
    }

    // Write storage_class column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let storage_values: Vec<_> = narinfo_hashes
            .iter()
            .map(|_| parquet::data_type::ByteArray::from("STANDARD"))
            .collect();
        col_writer
            .typed::<parquet::data_type::ByteArrayType>()
            .write_batch(&storage_values, None, None)?;
        col_writer.close()?;
    }

    row_group_writer.close()?;
    writer.close()?;

    Ok(())
}

/// Create test parquet files with mixed content (narinfo and non-narinfo files)
pub fn create_mixed_parquet_file(
    path: &Path,
    records: &[(&str, &str)], // (bucket, key) pairs
) -> Result<(), Box<dyn std::error::Error>> {
    let schema = Arc::new(parse_message_type(S3_INVENTORY_SCHEMA)?);
    let file = File::create(path)?;
    let writer_props = WriterProperties::builder().build();
    let mut writer = SerializedFileWriter::new(file, schema, Arc::new(writer_props))?;

    let mut row_group_writer = writer.next_row_group()?;

    // Write bucket column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let bucket_values: Vec<_> = records
            .iter()
            .map(|(bucket, _)| parquet::data_type::ByteArray::from(*bucket))
            .collect();
        col_writer
            .typed::<parquet::data_type::ByteArrayType>()
            .write_batch(&bucket_values, None, None)?;
        col_writer.close()?;
    }

    // Write key column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let key_values: Vec<_> = records
            .iter()
            .map(|(_, key)| parquet::data_type::ByteArray::from(*key))
            .collect();
        col_writer
            .typed::<parquet::data_type::ByteArrayType>()
            .write_batch(&key_values, None, None)?;
        col_writer.close()?;
    }

    // Write size column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let size_values: Vec<i64> = records
            .iter()
            .enumerate()
            .map(|(i, _)| 1000 + i as i64 * 100)
            .collect();
        let def_levels: Vec<i16> = vec![1; records.len()];
        col_writer
            .typed::<parquet::data_type::Int64Type>()
            .write_batch(&size_values, Some(&def_levels), None)?;
        col_writer.close()?;
    }

    // Write last_modified_date column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let date_values: Vec<i64> = records.iter().map(|_| 1700000000000).collect();
        let def_levels: Vec<i16> = vec![1; records.len()];
        col_writer
            .typed::<parquet::data_type::Int64Type>()
            .write_batch(&date_values, Some(&def_levels), None)?;
        col_writer.close()?;
    }

    // Write e_tag column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let etag_values: Vec<_> = records
            .iter()
            .map(|_| parquet::data_type::ByteArray::from("\"abc123\""))
            .collect();
        col_writer
            .typed::<parquet::data_type::ByteArrayType>()
            .write_batch(&etag_values, None, None)?;
        col_writer.close()?;
    }

    // Write storage_class column
    if let Some(mut col_writer) = row_group_writer.next_column()? {
        let storage_values: Vec<_> = records
            .iter()
            .map(|_| parquet::data_type::ByteArray::from("STANDARD"))
            .collect();
        col_writer
            .typed::<parquet::data_type::ByteArrayType>()
            .write_batch(&storage_values, None, None)?;
        col_writer.close()?;
    }

    row_group_writer.close()?;
    writer.close()?;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn test_create_test_parquet_file() {
        let temp_dir = TempDir::new().unwrap();
        let parquet_path = temp_dir.path().join("test.parquet");

        let hashes = vec!["hash1", "hash2", "hash3"];
        create_test_parquet_file(&parquet_path, &hashes).unwrap();

        assert!(parquet_path.exists());
        assert!(parquet_path.metadata().unwrap().len() > 0);
    }

    #[test]
    fn test_create_mixed_parquet_file() {
        let temp_dir = TempDir::new().unwrap();
        let parquet_path = temp_dir.path().join("mixed.parquet");

        let records = vec![
            ("nix-cache", "nar/hash1.narinfo"),
            ("nix-cache", "nar/hash2.nar"),
            ("nix-cache", "log/build.log"),
            ("nix-cache", "nar/hash3.narinfo"),
        ];

        create_mixed_parquet_file(&parquet_path, &records).unwrap();

        assert!(parquet_path.exists());
        assert!(parquet_path.metadata().unwrap().len() > 0);
    }
}
