{
  lib,
  stdenv,
  versionSuffix ? null,
  golangci-lint,
  buildGo124Module,
  rustfs,
  postgresql,
}:
let
  fs = lib.fileset;
in
buildGo124Module (final: {

  pname = "narwal";
  version = "0.1.0";

  # todo is there a way to avoid the `../../../`?
  src = fs.toSource {
    root = ../../..;
    fileset = fs.unions [
      ../../../cmd
      ../../../pkg
      ../../../go.mod
      ../../../go.sum
      ../../../main.go
    ];
  };

  vendorHash = "sha256-dsYMtZvLoAO0RcOJDQ+fNSo0L0wIpzFvN+8UiqmfVIE=";

  ldflags = [
    "-X github.com/numtide/narwal/pkg/build.Name=${final.pname}"
    "-X github.com/numtide/narwal/pkg/build.Version=v${final.version}${toString versionSuffix}"
    "-X github.com/numtide/narwal/pkg/build.System=${stdenv.hostPlatform.system}"
  ];

  doInstallCheck = true;

  # Needed for tests
  nativeCheckInputs = [
    postgresql
    rustfs
  ];

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
    description = "narwal: A Nix Binary Cache";
    homepage = "https://github.com/numtide/narwal";
    license = licenses.mit;
    mainProgram = "narwal";
  };
})
