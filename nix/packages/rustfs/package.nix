# Adapted from https://github.com/Mic92/niks3/blob/b6d346761001b8badf4a1dd7a29da18e6134779a/nix/packages/rustfs.nix
{
  lib,
  rustPlatform,
  fetchFromGitHub,
  pkg-config,
  protobuf,
  openssl,
}:
rustPlatform.buildRustPackage rec {
  pname = "rustfs";
  version = "1.0.0-alpha.72";

  src = fetchFromGitHub {
    owner = "rustfs";
    repo = "rustfs";
    rev = version;
    hash = "sha256-iWaZgvy40RW67oqyVttaWyrFrAVy17UJz5JydI51uDM=";
  };

  cargoHash = "sha256-ApVUUpeLXpMwqRnuNI/Q20/FTEvUyPTtDSpmPsDco2I=";

  nativeBuildInputs = [
    pkg-config
    protobuf
  ];

  buildInputs = [
    openssl
  ];

  # Only build the main rustfs binary
  cargoBuildFlags = [
    "--package"
    "rustfs"
  ];

  # Skip tests for now - they require a full test environment
  doCheck = false;

  meta = {
    description = "High-performance S3-compatible object storage";
    homepage = "https://rustfs.com";
    license = lib.licenses.asl20;
    mainProgram = "rustfs";
  };
}
