# goyt

Tiny web frontend for yt-dlp that runs as its own node on your tailnet (via tsnet).
Paste a link, pick video+audio or audio only, hit download. Defaults to best video + best audio.
Finished files show up on the page with download links.

## Run

    nix run . -- -dir ~/Videos/goyt

First run prints a Tailscale login URL in the log (or set `TS_AUTHKEY=tskey-...`).
Then open http://goyt/ from anything on the tailnet.

Flags: `-hostname`, `-dir`, `-state-dir`, `-https` (tailnet cert on :443), `-parallel`,
`-yt-dlp` (path to yt-dlp or youtube-dl), `-listen 127.0.0.1:8080` (skip tailscale, for local testing).

## NixOS

```nix
inputs.goyt.url = "github:scottjab/goyt";
# ...
imports = [ inputs.goyt.nixosModules.default ];
services.goyt = {
  enable = true;
  # authKeyFile = "/run/secrets/goyt-ts";  # file containing TS_AUTHKEY=tskey-...
  # downloadDir = "/srv/media/youtube";
};
```

If you bump Go deps, update `vendorHash` in flake.nix (set it to `lib.fakeHash`, build, copy the hash).
