{
  description = "goyt: a tiny yt-dlp web frontend";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAll (pkgs: {
        default = pkgs.buildGoModule {
          pname = "goyt";
          version = "0.1.0";
          src = ./.;
          vendorHash = null;
          nativeBuildInputs = [ pkgs.makeWrapper ];
          # yt-dlp needs ffmpeg to merge video+audio and extract audio.
          postInstall = ''
            wrapProgram $out/bin/goyt \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.yt-dlp pkgs.ffmpeg ]}
          '';
          meta.mainProgram = "goyt";
        };
      });

      devShells = forAll (pkgs: {
        default = pkgs.mkShell { packages = [ pkgs.go pkgs.yt-dlp pkgs.ffmpeg ]; };
      });

      nixosModules.default = { config, lib, pkgs, ... }:
        let cfg = config.services.goyt; in
        {
          options.services.goyt = {
            enable = lib.mkEnableOption "goyt yt-dlp web frontend";
            package = lib.mkOption {
              type = lib.types.package;
              default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
            };
            port = lib.mkOption {
              type = lib.types.port;
              default = 8080;
            };
            address = lib.mkOption {
              type = lib.types.str;
              default = "";
              description = "Address to bind. Empty means all interfaces.";
            };
            openFirewall = lib.mkOption {
              type = lib.types.bool;
              default = false;
              description = "Open the port on the tailscale0 interface only.";
            };
            downloadDir = lib.mkOption {
              type = lib.types.str;
              default = "/var/lib/goyt/downloads";
            };
          };

          config = lib.mkIf cfg.enable {
            systemd.services.goyt = {
              description = "goyt yt-dlp web frontend";
              wantedBy = [ "multi-user.target" ];
              after = [ "network-online.target" ];
              wants = [ "network-online.target" ];
              serviceConfig = {
                ExecStart = lib.escapeShellArgs ([
                  (lib.getExe cfg.package)
                  "-listen" "${cfg.address}:${toString cfg.port}"
                  "-dir" cfg.downloadDir
                ]);
                DynamicUser = true;
                StateDirectory = "goyt";
                Environment = [ "HOME=/var/lib/goyt" ];
                ReadWritePaths = lib.mkIf (!lib.hasPrefix "/var/lib/goyt" cfg.downloadDir) [ cfg.downloadDir ];
                Restart = "on-failure";
                NoNewPrivileges = true;
                ProtectSystem = "strict";
                PrivateTmp = true;
              };
            };
            networking.firewall.interfaces.tailscale0.allowedTCPPorts = lib.mkIf cfg.openFirewall [ cfg.port ];
          };
        };
    };
}
