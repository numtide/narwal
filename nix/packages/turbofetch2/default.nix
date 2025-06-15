{ pkgs, ... }:
pkgs.rustPlatform.buildRustPackage rec {
  pname = "turbofetch2";
  version = "0.1.0";

  src = ../../../turbofetch2;

  cargoLock = {
    lockFile = ../../../turbofetch2/Cargo.lock;
  };

  nativeBuildInputs = with pkgs; [
    pkg-config
  ];

  buildInputs =
    with pkgs;
    [
      openssl
    ]
    ++ lib.optionals stdenv.isDarwin [
      darwin.apple_sdk.frameworks.Security
      darwin.apple_sdk.frameworks.SystemConfiguration
    ];

  # Required for aws-sdk dependencies
  OPENSSL_NO_VENDOR = 1;
}
