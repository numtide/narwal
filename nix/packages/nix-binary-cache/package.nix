{
  lib,
  stdenv,
  versionSuffix ? null,
  golangci-lint,
  buildGo124Module,
}:
let
  fs = lib.fileset;
in
buildGo124Module (final: {

  pname = "nix-binary-cache";
  version = "0.1.0";

  # todo is there a way to avoid the `../../../`?
  src = fs.toSource {
    root = ../../..;
    fileset = fs.unions [
      ../../../pkg
      ../../../go.mod
      ../../../go.sum
    ];
  };

  vendorHash = "sha256-pxjtWD8bhu0/wLLnYUtIQ3W/UaiuzoxFmlXS6hYf5Jc=";

  ldflags = [
    "-X github.com/numtide/nix-binary-cache/pkg/build.Name=${final.pname}"
    "-X github.com/numtide/nix-binary-cache/pkg/build.Version=v${final.version}${toString versionSuffix}"
    "-X github.com/numtide/nix-binary-cache/pkg/build.System=${stdenv.hostPlatform.system}"
  ];

  doInstallCheck = true;

  env = {
    CGO_ENABLED = 0;
  };

  passthru.tests.golangci-lint = final.overrideAttrs (old: {
    nativeBuildInputs = old.nativeBuildInputs ++ [ golangci-lint ];
    buildPhase = ''
      HOME=$TMPDIR
      golangci-lint run
    '';
    installPhase = ''
      touch $out
    '';
  });

  meta = with lib; {
    description = "nix-binary-cache: A Nix Binary Cache";
    homepage = "https://github.com/numtide/nix-binary-cache";
    license = licenses.mit;
    mainProgram = "nix-binary-cache";
  };
})
