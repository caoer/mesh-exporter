{
  description = "Prometheus exporter for EasyTier mesh networks and sing-box proxies";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        version = self.shortRev or self.dirtyShortRev or "dev";
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "mesh-exporter";
          inherit version;
          src = ./.;
          vendorHash = null; # update after go mod tidy
          ldflags = [ "-s" "-w" "-X main.version=${version}" ];

          meta = with pkgs.lib; {
            description = "Prometheus exporter for EasyTier mesh and sing-box";
            homepage = "https://github.com/caoer/mesh-exporter";
            license = licenses.mit;
            mainProgram = "mesh-exporter";
          };
        };

        # Cross-compiled static binary for OpenWrt routers
        packages.arm64-static = pkgs.pkgsCross.aarch64-multiplatform.buildGoModule {
          pname = "mesh-exporter";
          inherit version;
          src = ./.;
          vendorHash = null;
          CGO_ENABLED = 0;
          ldflags = [ "-s" "-w" "-X main.version=${version}" "-extldflags=-static" ];
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go_1_24
            gopls
            golangci-lint
            gotools       # goimports
            delve         # debugger
            goreleaser
          ];

          shellHook = ''
            echo "mesh-exporter dev shell"
            echo "  go version: $(go version | cut -d' ' -f3)"
            echo "  lint:  golangci-lint run"
            echo "  test:  go test ./..."
            echo "  build: go build ./cmd/mesh-exporter"
          '';
        };
      }
    ) // {
      # NixOS module
      nixosModules.default = import ./nix/module.nix;
    };
}
