{
  description = "OpenClaw Dashboard — real-time bot monitoring UI (Go + Python)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        python = pkgs.python312;
        runtimeDeps = [ pkgs.bash pkgs.curl pkgs.jq pkgs.git ];
      in {
        packages = {
          # Go binary (default) — single binary, zero runtime deps
          default = pkgs.buildGoModule {
            pname = "openclaw-dashboard";
            version = "2026.3.3";
            src = ./.;
            vendorHash = null; # no external deps

            ldflags = [ "-s" "-w" ];

            nativeBuildInputs = [ pkgs.makeWrapper ];

            postInstall = ''
              mkdir -p $out/share/openclaw-dashboard
              cp ${./refresh.sh} $out/share/openclaw-dashboard/refresh.sh
              cp ${./themes.json} $out/share/openclaw-dashboard/themes.json
              chmod +x $out/share/openclaw-dashboard/refresh.sh
              wrapProgram $out/bin/openclaw-dashboard \
                --prefix PATH : ${pkgs.lib.makeBinPath runtimeDeps}
            '';

            meta = {
              description = "OpenClaw real-time bot monitoring dashboard (Go)";
              license = pkgs.lib.licenses.mit;
              platforms = pkgs.lib.platforms.unix;
              mainProgram = "openclaw-dashboard";
            };
          };

          # Python server (alternative)
          python-server = pkgs.stdenv.mkDerivation {
            pname = "openclaw-dashboard-python";
            version = "2026.3.3";
            src = ./.;

            nativeBuildInputs = [ pkgs.makeWrapper ];
            buildInputs = [ python ] ++ runtimeDeps;

            installPhase = ''
              mkdir -p $out/share/openclaw-dashboard $out/bin
              cp index.html server.py refresh.sh themes.json $out/share/openclaw-dashboard/
              chmod +x $out/share/openclaw-dashboard/refresh.sh
              makeWrapper ${python}/bin/python3 $out/bin/openclaw-dashboard \
                --add-flags "$out/share/openclaw-dashboard/server.py" \
                --prefix PATH : ${pkgs.lib.makeBinPath runtimeDeps}
            '';

            meta = {
              description = "OpenClaw real-time bot monitoring dashboard (Python)";
              license = pkgs.lib.licenses.mit;
              platforms = pkgs.lib.platforms.unix;
            };
          };
        };

        devShells.default = pkgs.mkShell {
          buildInputs = [
            pkgs.go
            python
            pkgs.bash pkgs.curl pkgs.jq pkgs.git
          ];
          shellHook = ''
            echo "OpenClaw Dashboard dev shell"
            echo ""
            echo "  Go:     go run . --port 8080"
            echo "  Python: python3 server.py --port 8080"
            echo "  Build:  go build -ldflags='-s -w' -o openclaw-dashboard ."
            echo "  Test:   python3 -m pytest tests/ -v"
          '';
        };

        apps = {
          default = flake-utils.lib.mkApp {
            drv = self.packages.${system}.default;
            exePath = "/bin/openclaw-dashboard";
          };
          python-server = flake-utils.lib.mkApp {
            drv = self.packages.${system}.python-server;
            exePath = "/bin/openclaw-dashboard";
          };
        };
      });
}
