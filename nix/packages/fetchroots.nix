{ inputs, system }:
let
  snix = import inputs.snix {
    localSystem = system;
  };
in
snix.contrib.fetchroots
