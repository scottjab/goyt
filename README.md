# goyt

Tiny web frontend for yt-dlp. Paste a link, pick video+audio or audio only, hit download.
Defaults to best video + best audio. Finished files show up on the page with download links.

## Run

    nix run . -- -listen :9090 -dir ~/Videos/goyt

Flags: `-listen` (default `:9090`), `-dir`, `-parallel`, `-yt-dlp` (path to yt-dlp or youtube-dl).

There's no auth, so keep it on a private network (e.g. only reachable over tailscale).

## NixOS

```nix
inputs.goyt.url = "github:scottjab/goyt";
# ...
imports = [ inputs.goyt.nixosModules.default ];
services.goyt = {
  enable = true;
  port = 9090;
  openFirewall = true;  # opens the port on tailscale0 only
  # downloadDir = "/srv/media/youtube";
};
```

If you bump Go deps, update `vendorHash` in flake.nix (set it to `lib.fakeHash`, build, copy the hash).
