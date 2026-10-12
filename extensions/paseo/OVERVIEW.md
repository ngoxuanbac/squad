Squad shows what your Squad multi-agent orchestrator is doing, inside Paseo. It is a read-only view, like `squad top`: open a Squad screen from the sidebar to see your teams and their members, solo sessions, recent events, and the latest lines of any member's log.

## What you see

- Counts of teams, sessions, workers and members working right now, with the Squad daemon's version and uptime.
- Each open team with its template, root directory, status and gate (the member that speaks for the team). Members show their role, whether they are a session you opened or a worker Squad started, their status, model, latest task and unread mail.
- Taskforces listed under the member or solo session that called them.
- Solo sessions that are in no team.
- Closed teams, folded into one line until you open them.
- The latest events, newest first.
- Select a member to follow its log. On a wide window the log opens beside the list; on a phone it opens in a sheet.

The screen refreshes every few seconds while a member is working, and less often when everything is idle. A member's log refreshes the same way.

## Setup

The plugin needs the `squad` command on the machine that runs the Paseo daemon. When Squad installs the plugin, it records its own path, so nothing else is needed. Otherwise the plugin runs `squad` from the Paseo daemon's PATH. To use another binary, set its full path in Settings under Squad (on Windows, point at `squad.exe`, not a script).

Requires Paseo 0.11.2 or newer and a Squad that supports `--view`. Works on Windows, macOS and Linux.

## How it reads Squad

The Paseo daemon runs `squad ps --view --no-start` and `squad tail <member> --view --no-start` directly, without a shell, and shows what they print. `--no-start` means looking never starts a Squad daemon. If the daemon is not running, the screen says so; run `squad ps` to start it. The plugin never sends mail, spawns, stops or closes anything, and it sends nothing off your machine.

If Squad and the plugin read different versions of the view, the screen says which one to update.
