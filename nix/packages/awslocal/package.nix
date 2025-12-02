{
  lib,
  buildPythonPackage,
  fetchPypi,

  # build-system
  setuptools,

  # dependencies
  localstack-client,
  awscli2,
}:

buildPythonPackage rec {
  pname = "awscli-local";
  version = "0.22.2";
  pyproject = true;

  src = fetchPypi {
    pname = "awscli_local";
    inherit version;
    hash = "sha256-B8Uyw3J1O/XxVCZFHckdbuyd6HeXSASTKamogr2sigs=";
  };

  build-system = [ setuptools ];

  dependencies = [
    localstack-client
    awscli2
  ];

  # No tests in the package
  doCheck = false;

  # No Python modules to import - just a script
  dontUsePythonImportsCheck = true;

  meta = {
    description = "Thin wrapper around the 'aws' command line interface for use with LocalStack";
    homepage = "https://github.com/localstack/awscli-local";
    license = lib.licenses.asl20;
    mainProgram = "awslocal";
    maintainers = [ ];
  };
}
