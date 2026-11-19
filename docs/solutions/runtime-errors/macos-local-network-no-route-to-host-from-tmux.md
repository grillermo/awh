---
title: "Go server gets \"no route to host\" to the camera when run from tmux (macOS Local Network privacy)"
date: 2026-11-19
category: runtime-errors
problem_type: runtime_error
component: serve / cmd/awh / cmd/lan-launcher
platform: macOS 15.7 (Sequoia), Mac mini
symptoms:
  - "ffmpeg: Connection to tcp://192.168.1.21:554?timeout=0 failed: No route to host"
  - "ffprobe: exit status 1 ... No route to host"
  - "Go net.Dial: connect: no route to host to any 192.168.1.x address"
  - "The Python server (app.py) reaches the same camera fine from the same tmux server"
root_cause: macos_local_network_privacy
tags: [macos, local-network, tmux, tcc, codesign, ffmpeg, launchservices, go]
commit: fdc1bd4
---

# "No route to host" from tmux: macOS Local Network privacy

## Problem

`./serve` (the Go server, `awh`) started in a tmux session on the Mac mini could
not reach any LAN host. The camera RTSP stream (`192.168.1.21:554`) and the Tapo
plug (`192.168.1.86`) both failed with `No route to host`, which surfaced as
ffmpeg/ffprobe errors in `[index]` and `[live]` capture. The Python server worked
from the same tmux server, which led to a rollback (commit `094bbc6`) before
this fix.

It is not a routing problem: `route`, ping from Terminal and the Python process all
worked. macOS 15's **Local Network privacy** returns `EHOSTUNREACH` to a process
it has not allowed to reach the LAN.

## Investigation (what was measured)

| Test, run from a shell inside tmux | Result |
|---|---|
| Python (`Python.app/Contents/MacOS/Python`) connect to `192.168.1.86:554` | reached host (`Connection refused`) |
| Bare Go binary (`go build`, ad hoc, `Identifier=a.out`) | `no route to host` |
| Homebrew `ffprobe` (bare binary) | `no route to host` |
| Go binary inside a signed `.app` bundle, **before** approval | `no route to host`, and no prompt shown |
| Same bundle launched with `open`, retrying for a while | prompt appeared, then `OK` after **Allow** |
| That approved bundle's binary run directly from tmux | `OK` |
| Same bundle **rebuilt** and re-signed with the Apple Development cert | `no route to host` again: the approval is tied to the exact binary, not to the bundle ID or team |
| Approved launcher run **from the shell**, spawning a child Go binary / ffprobe | child gets `no route to host` |
| Approved launcher started **with `open`**, spawning `sh` → `nc` | `OK` |
| `./serve` with the launcher via `open` → `awh` → `ffprobe`, then with a different `awh` build (`GOFLAGS=-ldflags=-buildid=...`) | camera frames served, 0 errors |

## Root cause

1. macOS 15 ties a Local Network approval to a specific code identity. An ad hoc
   signed binary has no stable identity (its designated requirement is its cdhash),
   and `./serve` ran `go build` on every start, so `awh` was always an unknown program.
   Even signing with an Apple Development identity did not keep an approval across
   rebuilds.
2. A program started from a tmux pane cannot show the "Allow … to find devices on
   your local network" prompt. macOS denies it silently, so there is never a
   prompt to click.
3. Child processes are checked against the process macOS holds **responsible** for
   them. A launcher started from a shell is not responsible for its children, but an
   app started by LaunchServices (`open`) is, so its children (awh, ffmpeg)
   inherit its approval.

Python worked because `Python.app` is a long-lived bundle that had been approved
in the past and isn't rebuilt.

## Solution

A tiny launcher app is built and approved **once** and never rebuilt. `./serve`
rebuilds `awh` freely and starts it **through the launcher with `open`**:

- `cmd/lan-launcher/main.go` runs its arguments as a child, passes TERM/HUP on to it and
  returns its exit code.
- `scripts/make-lan-launcher` builds `AWH LAN.app` (gitignored) with an `Info.plist`
  (`CFBundleIdentifier com.grillermo.awh.lan-launcher`, `LSUIElement`,
  `NSLocalNetworkUsageDescription`). It signs the app with the Apple Development
  identity, launches it with `open` and keeps probing the camera until **Allow** is
  clicked on the Mac mini's screen.
- `serve` / `serve-go`:

```bash
open -n --stdout "$log" --stderr "$log" "$app" --args /bin/sh -c '
  cd "$1" && shift
  echo $$ > logs/awh.pid
  set -a; . ./.env.awh; set +a
  export PATH="/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
  exec env GOMEMLIMIT=24MiB HOST="${HOST:-0.0.0.0}" PORT="${PORT:-3060}" ./awh "$@"
' sh "$PWD" "$@"
```

  then it follows `logs/awh.log` in the pane and kills the pid in
  `logs/awh.pid` on Ctrl-C, TERM or HUP. Killing the tmux session stops `awh`.

Details that matter:
- Apps started with `open` get launchd's environment, so `PATH` (to find
  ffmpeg/ffprobe) and `.env.awh` are set inside the child shell, not inherited.
- `open -W` together with `-n` failed ("Unable to block on application"), so
  scripts poll a log file instead of waiting on `open`.
- `awh` no longer depends on Terminal: closing Terminal leaves it running, and
  `~/c/server/after-reboot.sh` (`./serve && exit` in a detached session) works
  unchanged.

## Prevention / when it comes back

- If "no route to host" to a LAN address reappears, run `./scripts/make-lan-launcher`
  on the Mac mini **with someone at the screen** and click **Allow**. A second prompt
  showed up during the first `serve` run after the launcher was approved; it may
  have been for Homebrew's ffmpeg/ffprobe, so a `brew upgrade ffmpeg` might ask
  again.
- Rebuilding `cmd/lan-launcher` means approving it again, so keep it out of `./serve`.
- Any other Go or other compiled service in `after-reboot.sh` that talks to LAN
  devices will hit the same wall. Reuse this pattern: a launcher app approved once
  and started with `open`.
- Quick check from a tmux shell: if Python reaches a LAN host and a fresh
  `go build` binary doesn't, it is Local Network privacy, not the network.
- When a tool is run headless, macOS has nowhere to show the prompt. A screenshot
  (`screencapture -x`) shows whether a prompt is pending.

## Related

- Commit `094bbc6`: rollback to Python ("Go binary cannot reach the camera from tmux")
- Commit `fdc1bd4`: this fix
- Apple TN3179, *Understanding local network privacy*
