{
  description = "goyt: a tiny yt-dlp web frontend that lives on your tailnet";

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
          vendorHash = "sha256-fEmhwJPsVpmNyn7fMFA5SeCfTKVora8c+MgwVhGOoBc=";
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
            hostname = lib.mkOption {
              type = lib.types.str;
              default = "goyt";
              description = "Tailnet hostname for the node.";
            };
            downloadDir = lib.mkOption {
              type = lib.types.str;
              default = "/var/lib/goyt/downloads";
            };
            https = lib.mkOption {
              type = lib.types.bool;
              default = false;
              description = "Also serve HTTPS with a tailnet cert (HTTPS must be enabled for the tailnet).";
            };
            authKeyFile = lib.mkOption {
              type = lib.types.nullOr lib.types.path;
              default = null;
              description = "File with a Tailscale auth key (TS_AUTHKEY=tskey-...). Only needed for the first login; otherwise watch the journal for a login URL.";
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
                  "-hostname" cfg.hostname
                  "-state-dir" "/var/lib/goyt/tsnet"
                  "-dir" cfg.downloadDir
                ] ++ lib.optional cfg.https "-https");
                DynamicUser = true;
                StateDirectory = "goyt";
                Environment = [ "HOME=/var/lib/goyt" ];
                EnvironmentFile = lib.mkIf (cfg.authKeyFile != null) cfg.authKeyFile;
                ReadWritePaths = lib.mkIf (!lib.hasPrefix "/var/lib/goyt" cfg.downloadDir) [ cfg.downloadDir ];
                Restart = "on-failure";
                NoNewPrivileges = true;
                ProtectSystem = "strict";
                PrivateTmp = true;
              };
            };
          };
        };
    };
}
