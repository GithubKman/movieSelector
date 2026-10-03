{
  description = "movieSelector - lightweight movie/show request queue for a NAS";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "movieselector";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-7IC/p5GlD2EZkDXQzkaZ7E19S/ABKEBsg68vt8pykis="; # update after changing go.mod
          env.CGO_ENABLED = 0;
          ldflags = [ "-s" "-w" ];
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            sqlite # inspect the database: sqlite3 data/movies.db
            mailpit # local SMTP server + web inbox for testing digests
            curl
            jq
          ];
          shellHook = ''
            export CGO_ENABLED=0
            echo "movieSelector devshell: go $(go env GOVERSION | sed 's/go//')"
            echo "  make dev      - run server (demo catalog unless TMDB_API_KEY set)"
            echo "  make mail     - run mailpit (SMTP :1025, inbox http://localhost:8025)"
            echo "  make test     - run tests"
          '';
        };
      });
}
