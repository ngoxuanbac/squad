# squad guide

How to use squad day to day, by task. Every command and key is in [reference.md](reference.md).
Install is in the [README](../README.md).

## Concepts in one screen

- **Session**: a Claude Code agent you opened yourself, in a terminal, an IDE or Paseo. Once
  `squad setup claude` has run, every session starts its own `squad mcp` and joins squad as a
  **solo**, with four tools: `send`, `inbox`, `who`, `agent`.
- **Team**: a group of agents that share a template. A solo founds one when you ask ("found a
  lead-peer team for this") and becomes its **gate**: the one that talks to you and to other teams.
- **Role**: what one member may do. The template says who may write to whom, who may spawn whom,
  which tools each role has and how many workers may run at once. The daemon checks it on every
  mail and every spawn; prompts only advise.
- **Worker**: a headless Claude (`claude -p`) squad starts for a role. You can watch, abort, kill
  and resume it by name.
- **Mail**: how agents talk. A worker's mail is acked only when the turn that got it completes, so
  a crash, a kill or a reboot never loses it: it comes again.
- **Taskforce**: a temporary team called up for one job (a review, a hard decision). It answers
  its caller by mail and is closed by its caller.

## Run a team

1. `squad setup claude` once, then restart the Claude Code sessions that were open.
2. Open Claude Code (or a Paseo agent) in your project and ask: *"found a lead-peer team to fix
   the failing tests"*. The session lists the templates (`agent action=templates`), founds the
   team rooted at its directory and becomes its gate.
3. The gate spawns workers as its role allows, briefs each in `task` (their first mail), and ends
   its turn. With the hooks plugin (`squad setup claude`) it is woken when a handback comes and
   reads it with that turn; without it, it waits with `inbox wait_seconds=600`. You keep talking to
   the gate.
4. When the work is done, the gate closes the team (`agent action=close`), or you run
   `squad team down <team>`.

Pick a model per worker with `model=` on the spawn (`haiku` for small scopes is cheap), or per role
with `spawn.model` in the template.

## Call up a taskforce

**When**: a second opinion, a review or one hard decision, without leaving your session.

> *"Call a dual-lens taskforce to review this diff before I merge it."*

The session runs `agent action=spawn template=dual-lens task="…"`. squad builds the taskforce in
your directory (or `cwd`), starts its chair as a worker, and gives it the task as its first mail.
The chair answers you with `send to=caller`; read it with `inbox`. Close it with
`agent action=close team=<name>` when you are done.

- **Who may call one**: a solo, a team's gate, and any role whose template lists the taskforce in
  `can_call_taskforce`. The built-in `slp` lets a lane's lead call `dual-lens` and `advisor`, so a
  lead can get a review without going through the supervisor.
- **A taskforce never calls another one.**
- **It does not outlive its caller**: it closes when its caller's team closes, or when the
  session that called it goes away.
- The built-in ones are read-only by their prompts, not by squad.

## Watch and step in

```sh
squad ps                    # teams, members, solo sessions, latest events
squad top                   # the same, every 2 seconds
squad tail lead -n 50 -f    # a worker's log, readable; -f follows
squad abort lead            # cancel its current turn; it stays alive
squad kill lead             # stop its process tree; new mail resumes it
squad resume lead           # start it again in its Claude session
squad team down api         # close a team
squad model lead opus --thinking high   # switch its model or effort, from its next turn
```

In Paseo, the **Squad** screen (sidebar) shows the same: teams with their members, taskforces
under their caller, solos, events, and the log of the member you select.

**A mail was refused?** The agent got `refused (<code>): …` with what it may do instead: write to
the team's name to reach its gate (`not_gate`), ask its lead (`routing`), wait (`limit`).

## Between teams

Teams are isolated. The only channel between two teams, or between a team and a solo, is gate to
gate: writing to a team's name reaches its gate, and a member that is not the gate asks its gate to
relay. `who` lists your team, then one line per other team and solo.

## When things stop

- **A worker crashed or was killed**: it is `stopped`; its unacked mail waits. New mail resumes it
  in the same Claude session (at most 6 times an hour), or `squad resume <name>`.
- **The daemon stopped or the machine rebooted**: workers stopped with it (on Windows their job
  objects close with the daemon). The next command or session starts the daemon again; resume the
  workers that should go on, or send them mail.
- **Your session went away**: it is `gone` and stays in its team; mail for it waits. When Claude
  Code resumes that session, its `squad mcp` registers with the same `CLAUDE_CODE_SESSION_ID` and
  it is the same participant again, with its team, role and mail.
- **Taskforces** close when the session that called them goes away or the caller's team closes.

## Housekeeping

`~/.squad/config.yaml` holds the daemon's settings (every key is in
[reference.md](reference.md#settings-configyaml)). After an edit:

```sh
squad check     # reads config.yaml, the Claude profile and your templates; exit 1 on errors
squad restart   # the daemon reads config.yaml at its start
```

Closed teams and gone solo sessions are archived to `~/.squad/archive/` and deleted after
`gc.closed_after` (14 days); the daemon does it daily. By hand:

```sh
squad gc --dry-run                  # what would go
squad gc --closed-before 2d         # archive and delete what closed over 2 days ago
squad archive list
squad archive show <file>           # a collected team: members and mail
```

To add your own rules to every lead and peer, edit `~/.squad/rules/general-policy.md`, or name
your own files under `prompts:` in `config.yaml`.

`squad setup remove claude` and `squad setup remove paseo` take squad out again.

## Customize templates

```sh
squad template new mine --from slp     # ~/.squad/templates/mine/manifest.yaml + prompts/
```

Edit roles, routing and limits; a template is read when a team is founded from it, so a running
team keeps the one it started with. Every key is in
[reference.md](reference.md#template-keys-manifestyaml).
