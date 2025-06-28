#[path = "../../tests/test_utils.rs"]
mod test_utils;

use std::fs;
use std::path::Path;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Read the text fixtures
    let fixtures_dir = Path::new("tests/fixtures");

    // Read small-batch.txt
    let small_batch_content = fs::read_to_string(fixtures_dir.join("small-batch.txt"))?;
    let small_batch_hashes: Vec<&str> = small_batch_content.lines().collect();

    // Read medium-batch.txt
    let medium_batch_content = fs::read_to_string(fixtures_dir.join("medium-batch.txt"))?;
    let medium_batch_hashes: Vec<&str> = medium_batch_content.lines().collect();

    // Read large-batch.txt
    let large_batch_content = fs::read_to_string(fixtures_dir.join("large-batch.txt"))?;
    let large_batch_hashes: Vec<&str> = large_batch_content.lines().collect();

    // Create parquet files
    test_utils::create_test_parquet_file(
        &fixtures_dir.join("small-batch.parquet"),
        &small_batch_hashes,
    )?;
    println!(
        "Created small-batch.parquet with {} records",
        small_batch_hashes.len()
    );

    test_utils::create_test_parquet_file(
        &fixtures_dir.join("medium-batch.parquet"),
        &medium_batch_hashes,
    )?;
    println!(
        "Created medium-batch.parquet with {} records",
        medium_batch_hashes.len()
    );

    test_utils::create_test_parquet_file(
        &fixtures_dir.join("large-batch.parquet"),
        &large_batch_hashes,
    )?;
    println!(
        "Created large-batch.parquet with {} records",
        large_batch_hashes.len()
    );

    // Create a mixed content parquet file for testing filtering
    let mixed_records = vec![
        ("nix-cache", "nar/hash1.narinfo"),
        ("nix-cache", "nar/hash2.nar"),
        ("nix-cache", "log/build.log"),
        ("nix-cache", "nar/hash3.narinfo"),
        ("nix-cache", "realisations/abc.json"),
        ("nix-cache", "nar/hash4.narinfo"),
    ];

    test_utils::create_mixed_parquet_file(
        &fixtures_dir.join("mixed-content.parquet"),
        &mixed_records,
    )?;
    println!(
        "Created mixed-content.parquet with {} records",
        mixed_records.len()
    );

    Ok(())
}
