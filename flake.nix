{
  description = "aip-go - Go helpers for Google AIP resource patterns and List-RPC handling";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      nixpkgs,
      flake-utils,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          name = "aip-go";
          packages = [
            pkgs.go_1_27
            pkgs.buf
          ];
        };
      }
    );
}
