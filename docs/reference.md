# squad reference

Every command, file, environment variable, template key and agent tool. How to use them by task is
in [guide.md](guide.md); `squad help` lists the commands.

## Commands

Every command but `serve` and `mcp` takes `--no-start`: fail (exit 1, stderr
`the squad daemon is not running`) instead of starting the daemon. Flags may come before or after
the positional arguments.

| Command | What it does |
|---|---|
| `setup` | Where each integration stands: Claude Code (is MCP server `squad` added), Paseo (is plugin `squad` installed), the home directory |
| `setup claude` | `claude mcp add --scope user squad -- <squad> mcp`, and the hooks plugin `squad@squad` from a local marketplace in `~/.squad/claude` (`claude plugin marketplace add`, `claude plugin install`); run again it installs both afresh. Restart open Claude Code sessions |
| `setup paseo` | Write the embedded plugin to `~/.squad/paseo` (naming this binary in `server/installed.ts`) and `paseo plugin install` it (an earlier `squad` plugin is removed first). Reload the Paseo app |
| `setup remove claude\|paseo` | Take squad out: `claude mcp remove`, `claude plugin uninstall squad@squad` and its marketplace, then delete `~/.squad/claude`; or `paseo plugin remove squad` and delete `~/.squad/paseo`. Workers are unaffected (each gets its own MCP config) |
| `ps [--json\|--view]` | Daemon, teams with their members, solo sessions and the latest events, once. `--json` and `--view` print the view document below |
| `top` | `ps`, refreshed every 2 seconds |
| `tail <name> [-n N] [-f] [--view\|--json]` | A worker's log as readable lines (`-n`, default 40); `-f` follows; `--view` the tail document below; `--json` the raw stream-json lines |
| `kill <worker>` (short: `x`) | Stop the worker's process tree: its stdin is closed, then Ctrl+Break and the job object is terminated (Windows) or SIGTERM and SIGKILL go to its process group (Unix), within 4 seconds. New mail to it resumes it |
| `abort <worker>` | Cancel its current turn; it stays alive. Mail of a cancelled turn is given again |
| `resume <worker>` | Start a stopped worker again in its Claude session; its unacked mail comes again. A parked worker resumes once its team's `max_respawn_per_hour` allows |
| `why <from> <to>` | Every check a send from `from` to `to` would pass or fail (permission, route, routing rule, limits, cc), and the verdict `allow`, `deny` or `hold`; sends nothing |
| `log [--after N] [--team T] [--limit N] [--json]` | The event log: found, spawn, send, deny, held, released, notice, gate_moved, rerouted, admitted, watch_fired, parked, … |
| `doctor [--json]` | What looks stuck: `stuck_turn` (working over 30 min with no turn end), `gone_unacked`, `held`, `no_process`, `parked`; exit 1 when anything does |
| `release #N` | Deliver a message held by the rate limit, and its cc copies |
| `model <worker> [model] [--thinking L]` | Set a worker's model, effort level or both. The level must be one Claude runs (`low`, `medium`, `high`, `xhigh`, `max`): Claude takes any level live but fails on a bad one at the next start. A running worker switches first (Claude `set_model`, then `apply_flag_settings` effortLevel), from its next turn with its context kept; if Claude refuses, nothing is stored (`nothing stored: …`). The stored values are what its next start uses. A session's model is chosen in the session |
| `gc [--closed-before 14d] [--dry-run] [--json]` | Archive, read back, then delete each team closed (and each solo session gone) longer than `--closed-before` (default `gc.closed_after`): its rows, mail, timer marks, worker logs and run scratch. A unit whose archive cannot be written or read back the same is kept (`kept: …`). Archives older than `gc.archive_keep` are deleted. The daemon runs it at its start and every 24 hours while `gc.closed_after` is on. A gone session an open team names as gate or caller is kept |
| `archive list` | The archives in `~/.squad/archive/` |
| `archive show <file> [--json]` | An archive as text (team, members, mail), or as it is. Needs no daemon |
| `check` | Read `config.yaml`, the Claude profile, your templates and the files `config.yaml` names, as the daemon will at its start: errors (exit 1: a bad key or value, a harness other than `claude`, a thinking level Claude does not run in the profile or a role, a template that does not load) and warnings (a prompt file that cannot be read, an allowed root that is not a directory, `claude` not on PATH). Needs no daemon |
| `team down <team>` | Close a team or taskforce: workers stopped, sessions back to solo, the taskforces its members called closed too |
| `template list` | Name, origin (`built-in` or `yours`), whether it is a taskforce, summary |
| `template new <name> --from <built-in>` | Copy a built-in into `~/.squad/templates/<name>/` (manifest and prompt files) |
| `stop` | Stop the daemon and every worker |
| `restart` | Read `config.yaml` (refuse when it is bad), stop the daemon, wait until it let go of `squad.lock`, start it again. Workers stop; mail resumes them |
| `serve` | Run the daemon in the foreground. Commands start it detached when none answers |
| `mcp` | The stdio MCP server Claude Code runs (`setup claude` adds it) |
| `hook claude <HookEvent>` | What the plugin's hooks run, with the hook's JSON on stdin; prints Claude's answer. Never fails: no daemon, or an error, prints nothing. Nothing in a worker (`SQUAD_WORKER=1`) |
| `version` | The binary's version |

## Files (`~/.squad`, or `$SQUAD_HOME`)

| Path | Yours to edit? | What it is |
|---|---|---|
| `config.yaml` | yes | The daemon's settings (below); written with the defaults when missing, read at its start |
| `rules/general-policy.md` | yes | The shared prompt the default `config.yaml` adds to the `lead-peer` and `slp` role cards; written when missing |
| `templates/<name>/manifest.yaml` | yes | Your templates; a name here shadows the built-in of that name |
| `harness/claude.json` | yes | How Claude workers start (below); written with the defaults when missing, read at each spawn and resume |
| `serve.log` | read | The daemon's log: where problems are written |
| `squad.db` | no | State: teams, participants, mail, events (SQLite, WAL); its schema version is `PRAGMA user_version`, brought up to date at each start |
| `daemon.json` | no | The running daemon's address and admin token (removed when it stops) |
| `squad.lock` | no | The pid of the daemon that runs; one daemon per home |
| `logs/<id>.jsonl` | no | Each worker's stream-json output and the mail it was given |
| `run/<id>/` | no | Scratch of a worker's runs (its MCP config) |
| `archive/<time>-<kind>-<name>.json` | read | What `gc` collected: every row of the team or solo, table by table |
| `claude/` | no | The hooks plugin and its local marketplace (`setup claude`) |
| `paseo/` | no | What `setup paseo` installed |

## Environment

| Variable | Read by | Meaning |
|---|---|---|
| `SQUAD_HOME` | every command | Squad's home instead of `~/.squad` |
| `CLAUDE_CODE_SESSION_ID` | `squad mcp` | Set by Claude Code. A session that registers again with the same id is the same participant (name, team, role, mail) |
| `SQUAD_TOKEN` | `squad mcp` | Set by squad for a worker's MCP server: whose tools these are |

Workers inherit the daemon's environment minus the variables of the session that started it
(`CLAUDECODE`, `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_MESSAGING_*`, `PASEO_AGENT_ID`, …), plus
`SQUAD_WORKER=1` and `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1`. So `ANTHROPIC_BASE_URL` and the like
reach workers when the daemon has them.

## Settings (`config.yaml`)

Read at the daemon's start: a change needs `squad restart`, and `squad check` reads it as the
daemon will. An unknown key or a bad value stops the daemon (the reason is in `serve.log`); a key the
file leaves out keeps its default.

| Key | Default | Meaning |
|---|---|---|
| `harness` | `claude` | The harness of a worker whose role and founding session name none |
| `gc.closed_after` | `14d` | `gc` collects a team closed (or a solo session gone) longer than this: `14d`, `36h`, `90m`, or `off` (no automatic gc; `squad gc --closed-before` still works) |
| `gc.archive_keep` | `30d` | Archives older than this are deleted at each gc, or `off` |
| `spawn.allowed_roots` | `[]` | Absolute directories outside a team's root where a worker may work (`spawn cwd=`); the root and its repository's git worktrees always may. Elsewhere: refused, rule `cwd.bounds` |
| `prompts` | `rules/general-policy.md` for `lead-peer/*` and `slp/*` | Your files added to role cards under `## Shared prompt from <file>`. `file` is relative to squad's home or absolute; `roles` are `<role>` (any template), `<template>/<role>`, `<template>/*`, `solo` (a solo session's card), or `*`. A file is read at each worker start and session start, so an edit shows in the next; one that cannot be read is left out and written to `serve.log`. `prompts: []` adds none |

## Harness profile (`harness/claude.json`)

| Key | Default | Meaning |
|---|---|---|
| `cmd`, `args` | `claude`, `[]` | What runs, and extra flags after squad's |
| `env` | `[]` | Extra `KEY=VALUE` for the worker |
| `model`, `thinking` | `inherit` | The worker's model and effort when its spawn and role name none; `inherit` leaves it to Claude |
| `disallowed_tools` | the tools that wait on a person, start agents or schedule turns outside squad (`AskUserQuestion`, `Agent`, `Task`, `EnterPlanMode`, `ScheduleWakeup`, `Monitor`, …) | Passed as `--disallowedTools`; a role keeps one with `spawn.allow_tools` |
| `blacklist` | `[]` | Parts of your Claude setup a worker must not load: MCP servers of `~/.claude.json` by name, plugins as `name@marketplace` |

A key the file leaves out keeps its default.

## Workers

A worker runs on the harness its role names (`spawn.harness`), else its gate's, else the daemon's
default (Claude; Codex is planned). Its model is the spawn's `model`, else the role's
`spawn.model`, else the profile's; its effort the role's `spawn.thinking`, else the profile's.

A Claude worker is `claude -p --input-format stream-json --output-format stream-json --verbose
--permission-mode bypassPermissions --strict-mcp-config --mcp-config <your user MCP servers minus
the blacklist, plus squad's> --setting-sources user --disallowedTools <profile minus allow_tools>`,
with `--session-id` (new) or `--resume` (again), `--model` and `--effort`, and the role card
appended to the system prompt. Its directory is the spawner's, or
`cwd` when the role has `can_set_cwd` (inside the team root or a git worktree of its repository).

- Mail is written to stdin as one user message per batch. The batch is acked only when its
  `command_lifecycle` says `completed`; `cancelled` and a process that exits give it again.
- Mail for a worker in a turn is not written to stdin: the inbox tool takes it, or it starts the
  next turn. Working state comes from `session_state_changed`, with `command_lifecycle started` and
  `result` as a fallback.
- A stopped worker of an open team is resumed when mail comes for it, at most 6 times an hour
  (then `squad resume` by hand).
- On Windows each worker runs in a job object that is killed with the daemon; on Unix in a
  process group of its own, and on Linux the kernel kills it when the daemon dies, however it dies.

## Template keys (`manifest.yaml`)

| Key | Meaning |
|---|---|
| `template` | Its name |
| `summary` | One line for `template list` and the agent tool |
| `auto_join_role` | The founder's role (and a taskforce's chair); may be left out when there is one role |
| `taskforce.idle_for` | A `taskforce:` block makes the template callable with `spawn template=`; after `idle_for` with nobody working, its caller is told (once per idle stretch) to close it |
| `roles.<r>.description` | One line in the role card |
| `roles.<r>.instructions` / `instructions_file` | The role's prompt; the file is relative to the template and inlined when the team is founded |
| `roles.<r>.tools` | Any of `send`, `inbox`, `who`, `agent` |
| `roles.<r>.can_spawn` | Roles it may start workers of |
| `roles.<r>.can_call_taskforce` | Taskforce templates it may call although it is not its team's gate |
| `roles.<r>.can_set_cwd` | It may give a worker a `cwd` |
| `roles.<r>.spawn.harness` | Harness of its workers (`inherit` or empty: its gate's, else the default) |
| `roles.<r>.spawn.model` / `spawn.thinking` | Model and effort of its workers (`inherit` or empty: the profile's, else Claude's default); a spawn's `model` wins |
| `roles.<r>.spawn.allow_tools` | Tools the profile disallows that this role's workers keep |
| `routing[]` | `{from: <role>, to: <role>, allow: true|false, cc: [roles]}`: the first rule matching the sender's and recipient's roles decides (`allow` left out is true; no rule denies); `cc` roles get a copy of what the rule lets through (not the sender or recipient). Spawning a role needs a rule from the spawner's role to it |
| `timers[]` | `{on: <role>, silent_for: 20m, notify: <role|reports_to>}`: a member of `on` working for longer than `silent_for` with no turn end is reported once per working stretch, by mail from the engine |
| `roles.<r>.can_pin` | It may pin to the team board |
| `limits.depth` | How deep spawn chains may go (0: no limit) |
| `limits.concurrency` | Live workers at once (0: no limit) |
| `limits.messages_per_participant_per_minute` | Per member, a sliding minute (copies not counted); over it mail is stored but held until `squad release #N`, and the sender is told once a minute |
| `limits.max_respawn_per_hour` | A worker resumed that often in an hour is parked (not resumed by mail) and the member it reports to is told; `squad resume` once the hour allows |

A limit is a number or `none`. A role with `can_spawn` needs `depth` and `concurrency`.

Unknown keys are refused when a template is read. A template is frozen into a team when it is
founded: editing it changes the next team only.

## Sessions (Claude Code with the hooks plugin)

A session you open joins squad twice over: `squad mcp` (its tools) and the hooks of `squad@squad`,
both naming it by Claude's session id. With the hooks it has turns:

| Hook | squad |
|---|---|
| `SessionStart` | The session joins squad (as a solo if new). After `/clear` or a compaction it is told its role card again |
| `UserPromptSubmit` | A turn starts: its mail is added to the prompt (`additionalContext`) |
| `PostToolUse` | Mail that came since is added after the tool call |
| `Stop` | Mail that came since holds the turn's end (`decision: block`, at most 3 times a turn) so the model reads it; otherwise the turn's mail is acked |
| `StopFailure`, `Notification: idle_prompt` (after Esc) | The turn is over unacked: its mail comes again |
| `PermissionRequest` | `permission`: its mail waits and it is not woken until a tool runs |
| `SessionEnd` | `gone` (not after `/clear`); the taskforces it called close |
| `PreToolUse` of `mcp__squad__*` | Allowed without a prompt |

When mail comes for an idle session, its `squad mcp` writes `[squad] wake #N` into Claude's
messaging socket (`CLAUDE_CODE_MESSAGING_SOCKET`: a named pipe on Windows), at most once every 15
seconds; Claude starts a turn and the mail comes with it. A session without the plugin reads its
mail with the inbox tool, which acks at once.

Mail for a **gone** member of an open team (not its gate) resumes that session as a headless
worker (`claude --resume <its session id>`, in its directory): it shows as a worker from then on.
One mail wakes it once; only newer mail wakes it again. When its person opens the session again
(same session id), the worker is stopped and it is a session again, with its team, role and mail.
A gone gate, a solo and a member of a closed team are never woken: their mail waits.

## Agent tools (`squad mcp`)

| Tool | Arguments | What it does |
|---|---|---|
| `send` | `to`, `body`, `kind`, `reply_to`, `op`, `target` | Mail to a member by name (by routing), to `caller` (a taskforce's chair), gate to gate to a team by its name or a solo, or to `board`. `kind` is a free label; `reply_to` the #N of mail you sent or got (else `reply_to.not_in_view`); `op=assign` marks the task of a member that reports to you (its task in ps); board: `op=replace|remove target=#N`. Answers `sent #N`, `duplicate of #N`, or `stored #N but HELD by <rule>` |
| `inbox` | `wait_seconds` (0–600), `view` | Unread mail; waits for mail when there is none. Acked at once, or, for a hooked session in a turn, when the turn ends normally. `view=board`: the team's pins, read-only |
| `who` | | You, your team (whom you may write to), other teams and solos |
| `agent` | `action=templates` | The templates |
| | `action=found template=T [name=N]` | A solo session becomes the gate of a new team |
| | `action=spawn role=R task=… [name] [cwd] [model]` | A worker; `task` is its first mail |
| | `action=spawn template=T task=… [name] [cwd] [model]` | A taskforce: a solo, a gate, or a role with `can_call_taskforce` calls it; it answers by mail |
| | `action=close [team=N]` | The gate closes its team; a caller closes a taskforce it called |
| | `action=stop name=N` | Stop a worker you spawned (the gate: any worker of its team) |
| | `action=team` | Your team as text |
| | `action=admit name=S role=R` | A solo session at the team root joins with role R; any member whose role may spawn R may admit; it becomes the gate if the team has none |
| | `action=reopen team=T` | A solo session at a closed team's root reopens it as its gate; workers stay stopped; mail for the old gate comes to it |
| | `action=resume name=W` / `action=tail name=W` | Start a stopped worker you spawned (the gate: any of its team) again; read its last lines |

A member that founds another team leaves its team: its mail from that team and the workers that
reported to it go to the gate; if it was the gate, the next member (sessions first, then in join
order, whose role has send) becomes the gate; a team left without a live gate gets a `gate_lost`
notice. The taskforces it called close.

A refusal comes back as a tool error `refused (<rule>): <why and what you may do instead>`, the rule
being the one that refused (`routing[2]`, `routing.no_rule`, `assign.not_reports_to`,
`team_gate.sender_not_gate`, `limits.concurrency`, `can_pin`, `board.full`, …) or a general code.

Mail reads to a model as `[squad] N new messages:` then one block per message,
`<message id="#N" from="<label>" sender="<name>" [kind=…] [reply_to="#M"] [cc_of="#M" cc_of_mail_to=…] at="15:04:05" [age="4m"] [redelivered="true">]`.
The label says who the sender is to the reader: `engine`, `x (solo)`, `x (role, team T)`,
`x (role, you report to them)`, `x (role, reports to you)` or `x (role)`. #N is one counter for all
mail.

The engine writes mail too (`from="engine"`): a hold notice (once a minute), a watch rule, a parked
worker to its lead, a taskforce idle or closed.

## Board

A team's pins: `send to=board body=…` (role `can_pin`), at most 20 live; `op=replace target=#N`
puts a new pin in its place, `op=remove target=#N` takes it off. Nobody is woken by a pin; members
read them with `inbox view=board`.

## Notices

What only squad knows, for the person watching (Paseo, `ps --view`): `reply` (the gate finished a
turn on mail and sent nothing), `settled` (it sent its last message and nobody works or has mail
waiting), `failed` (its turn on mail failed), `gate_lost` (a member left while the gate is not
live, or there is none). Only a hooked gate's turns write the first three.
Every tool result but inbox's ends with `[squad] N unread message(s)` when mail waits.

## View documents (Paseo plugin)

What `squad ps --view` and `squad tail --view` print, for the Paseo plugin (`extensions/paseo`).
The Go types in `internal/view` are the source of truth. Both documents carry `version`; the
plugin refuses a version it does not read and says which side to update. Bump `version` on any
change that is not purely additive. The plugin runs both with `--no-start`, so looking never
starts a daemon.

### `squad ps --view`

```json
{
  "version": 1,
  "daemon": { "version": "0.1.2", "pid": 1234, "started_at": "2026-10-11T08:00:00Z" },
  "summary": { "teams": 1, "sessions": 1, "workers": 3, "working": 1 },
  "teams": [
    {
      "id": "t_9f2c",
      "name": "api",
      "template": "slp",
      "root": "C:\\Users\\me\\code\\api",
      "status": "open",
      "gate": "api",
      "parent": "",
      "created_at": "2026-10-11T08:01:00Z",
      "members": [
        {
          "id": "p_a1b2",
          "name": "lead-1",
          "role": "lead",
          "kind": "worker",
          "status": "working",
          "model": "sonnet",
          "reports_to": "api",
          "task": "First line of the latest assignment it got",
          "unread": 0,
          "cwd": "C:\\Users\\me\\code\\api",
          "last_active": "2026-10-11T08:05:00Z"
        }
      ]
    }
  ],
  "solos": [],
  "events": [
    { "at": "2026-10-11T08:02:00Z", "kind": "spawn", "team": "api", "text": "api spawned lead-1 (lead)" }
  ],
  "notices": [
    { "at": "2026-10-11T08:09:00Z", "age": "2m", "where": "api", "kind": "settled", "body": "api (gate of api) sent its last message and the team is settled: ..." }
  ]
}
```

- `teams`: open teams first, then closed ones (newest first). A taskforce has `parent` set to the
  name of the participant that called it; the plugin shows it under that caller.
- `status` of a team: `open` | `closed`.
- `kind` of a member: `session` (an agent the human opened) | `worker` (headless, squad started it).
- `notices`: the latest 5, newest first; `where` is the team, else `-`.
- `status` of a member: `working` (in a turn) | `idle` (live, waiting for mail) | `permission`
  (a session waiting on its person's answer to a permission prompt) | `parked` (a worker over its
  respawn limit) | `stopped`
  (worker process not running) | `gone` (session closed).
- `unread`: for a worker, mail not handed to its process yet; for a session, mail not read.
- `gate`: the member name that talks for the team.
- `solos`: sessions in no team, same shape as a member (`role` and `reports_to` empty).
- `events`: the latest 50, newest last. `kind` is one of `found`, `spawn`, `send`, `deny`,
  `stop`, `close`, `exit`, `resume`.
- Timestamps are RFC 3339 UTC; empty string when unknown.

### `squad tail <name> [-n N] --view`

```json
{
  "version": 1,
  "lines": [
    { "kind": "mail", "text": "[squad] from api (supervisor): fix the flaky test" },
    { "kind": "assistant", "text": "Looking at the test first." },
    { "kind": "tool", "text": "Bash", "rest": "go test ./..." },
    { "kind": "result", "text": "turn done (12.3s)" }
  ]
}
```

`kind` is one of `mail`, `user`, `assistant`, `thinking`, `tool`, `tool_result`, `result`,
`system`, `error`. `rest` is optional detail (a tool's input, a result's output), already cut short.
