# herdr integration notes (for M4)

Answers the open questions in `SPEC.md` § "herdr integration". Researched against **herdr 0.9.3**
(`herdr --version`), protocol 22, on 2026-09-30, from inside a herdr pane. Sources: `herdr --help`
and subcommand help, read-only live commands, `herdr api schema --json`, <https://herdr.dev/llms.txt>
(index), and the 0.9.3 `cli-reference.mdx` / `socket-api.mdx` it links to.
`herdr.dev/agent-guide.md` is high-level only and has none of this.

Legend: **[verified]** = I ran it and quote the output. **[docs]** = from the docs only.
**[unverified]** = neither.

## 1. Environment and identity

### Env vars injected into a pane [verified]

```
$ env | grep HERDR
HERDR_PANE_ID=w3:p3
HERDR_BIN_PATH=/home/user/.local/bin/herdr
HERDR_ENV=1
HERDR_TAB_ID=w3:t1
HERDR_SOCKET_PATH=/home/user/.config/herdr/herdr.sock
HERDR_WORKSPACE_ID=w3
```

| Var | Meaning |
|---|---|
| `HERDR_ENV` | `1` inside any herdr-managed pane process. The "am I in herdr" flag. |
| `HERDR_PANE_ID` | Pane id, e.g. `w3:p3`. |
| `HERDR_TAB_ID` | Tab id, e.g. `w3:t1`. |
| `HERDR_WORKSPACE_ID` | Workspace id, e.g. `w3`. |
| `HERDR_SOCKET_PATH` | Server socket. The CLI uses it, so `herdr ...` from a pane reaches the right session. |
| `HERDR_BIN_PATH` | Absolute path of the herdr binary. Prefer it over `PATH` lookup. |

Also set [docs]: `TERM_PROGRAM=herdr`, `TERM_PROGRAM_VERSION`. Not injected into normal panes: `HERDR_SESSION`
(a user-set CLI selector). `HERDR_PLUGIN_*` only appears for plugin-launched processes.

**There is no agent-name env var.** The agent name is not exported.

Notes:
- IDs are workspace-qualified (`w<N>:p<M>`) and look like short strings, not UUIDs.
- They are fixed at launch. After `pane move` the process keeps its old ids; herdr keeps an alias [docs]. Treat them as a
  snapshot taken when `devrun up` ran, and re-resolve through the API if you need current ids.
- Child processes inherit them, including `setsid` children, so a detached server keeps them
  **[verified]**: a `setsid bash -c` child printed `HERDR_PANE_ID=w3:p3` and `/proc/<pid>/environ` contained it.

### Learning pane / workspace / tab / agent name

1. Read `HERDR_PANE_ID`, `HERDR_TAB_ID`, `HERDR_WORKSPACE_ID` from the environment. Cheap, no subprocess.
2. For the agent name and agent kind, ask the server (one call, no args needed from inside a pane):

```
$ herdr pane current
{"id":"cli:pane:current","result":{"pane":{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"<uuid>"},"agent_status":"working","cwd":"/home/user/code/devrun-wt/herdr-notes","focused":false,"foreground_cwd":"/home/user/code/devrun-wt/herdr-notes","pane_id":"w3:p3","revision":3,"scroll":{...},"tab_id":"w3:t1","terminal_id":"term_65cbf1752c3b07","terminal_title":"◑ Herdr integration documentation for M4","terminal_title_stripped":"Herdr integration documentation for M4","workspace_id":"w3"},"type":"pane_current"}}
```

`pane current` has `agent` (kind: `"claude"`) but **not** the user-visible agent name. The name is only on the agent
record, as `name`:

```
$ herdr agent get herdr-notes
{"id":"cli:agent:get","result":{"agent":{"agent":"claude",...,"name":"herdr-notes","pane_id":"w3:p3","tab_id":"w3:t1","workspace_id":"w3",...},"type":"agent_info"}}
```

`agent list` / `api snapshot` `.agents[]` carry `name` too. Observed names: `core`, `herdr-notes`. Panes whose agent was
never named have **no `name` key** (the `w2:p2` and `w3:p1` agents above). So the lookup is: find `.agents[]` where
`pane_id == $HERDR_PANE_ID`, take `.name` if present, else fall back to `.agent` (kind).

`owner` in the registry should therefore be: `herdr_pane` = `HERDR_PANE_ID`, `herdr_workspace` = `HERDR_WORKSPACE_ID`,
`herdr_tab` = `HERDR_TAB_ID` (new, cheap), `agent` = name if present else kind.

Workspace label (human name, e.g. `port-manager`) is only in `api snapshot` `.workspaces[].label`.

## 2. `herdr notification show`

### Syntax [verified, `--help` and docs agree]

```
herdr notification show [OPTIONS] <TITLE>
      --body <TEXT>
      --position <top-left|top-right|bottom-left|bottom-right>
      --sound <none|done|request>
```

- Title is required and positional. Body is optional.
- **No URL field, no severity field.** The only way to carry a URL, project or worktree is to put it in the title/body
  text. `--sound request` is the closest thing to a severity (plays the needs-attention sound); default is `none`.
- Limits [docs]: newlines/tabs/repeated whitespace collapse to single spaces; title is trimmed to **80 chars**, body to
  **240 chars**. An empty-after-sanitising title is an `invalid_params` error.
- `--position` only applies when `ui.toast.delivery = "herdr"`; terminal/system/off delivery ignore it [docs].
- Delivery follows the user's `[ui.toast]` config (herdr toast, terminal, system, or off) [docs].

### Output [verified, one call]

```
$ herdr notification show "devrun test" --body "herdr-notes research" --sound none
{"id":"cli:notification:show","result":{"reason":"shown","shown":true,"type":"notification_show"}}
$ echo $?
0
```

Output is always JSON on stdout. Parse `.result.shown` (bool) and `.result.reason`. Reasons [docs]: `shown`, `disabled`
(delivery is `off`), `rate_limited`, `no_foreground_client`, `busy` (an existing toast was not replaced). `shown:false`
should still exit 0 [unverified, I only triggered `shown`], so check the field and don't rely on the exit code to know
the user saw it.

Suggested devrun layout (fits the 80/240 limits):

```
herdr notification show "<project>/<worktree>: inspect :41237" --body "http://127.0.0.1:41237 <message>" --sound request
```

Put the URL first in the body so truncation drops the message tail, not the URL.

### No server running [verified for other commands, unverified for `notification show` itself]

I ran `notification show` once only, as instructed, so its no-server path was not exercised directly. The same
socket-connect path for other commands, with `HERDR_SOCKET_PATH` pointed at a missing socket:

```
$ HERDR_SOCKET_PATH=/nonexistent/herdr.sock herdr pane list
{"id":"cli:pane:list","error":{"code":"server_not_running","message":"no herdr server is running at /nonexistent/herdr.sock; run `herdr` to start or attach it"}}
$ echo $?
1
```

`api snapshot` gave the identical error. Expect `notification show` to do the same: **exit 1, JSON on stdout with
`.error.code == "server_not_running"`** (not on stderr). Confirm in an M4 test with a fake `HERDR_SOCKET_PATH`.
Note it does **not** auto-start a server.

## 3. Mapping a PID to a pane

### `herdr pane process-info` [verified]

```
$ herdr pane process-info --current
{"id":"cli:pane:process_info","result":{"process_info":{"foreground_process_group_id":2926680,"foreground_processes":[{"argv":["claude"],"cmdline":"claude","cwd":"/home/user/code/devrun-wt/herdr-notes","name":"claude","pid":2926680}],"pane_id":"w3:p3","shell_pid":2925051},"type":"pane_process_info"}}
```

Flags: `--pane <ID>` or `--current`. One pane per call. It reports only the pane's **shell PID** and its
**foreground process group** (pgid + processes in it).

**It cannot map an arbitrary devrun server PID to a pane.** devrun starts servers with `setsid`, so the server is in its
own session and process group, never the pane's foreground group. Test: after `setsid sleep 20`, `process-info` still
listed only `claude` (pgid 2926680); the `sleep` did not appear. `api snapshot` has no PIDs at all (see below).

### `herdr api snapshot` [verified]

`herdr api snapshot` prints `.result.snapshot` with top-level keys `agents, focused_pane_id, focused_tab_id,
focused_workspace_id, layouts, panes, protocol, tabs, version, workspaces`. No pid/pgid/shell_pid anywhere in
`agents`/`panes`. Sample (trimmed):

```json
{"id":"cli:api:snapshot","result":{"snapshot":{
  "agents":[{"agent":"claude","agent_status":"working","cwd":"/home/user/code/myapp","name":"core",
             "pane_id":"w3:p2","tab_id":"w3:t1","workspace_id":"w3","terminal_id":"term_0000000000000",
             "agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"25f9ff82-..."},
             "state_change_seq":29,"interactive_ready":true,"foreground_cwd":"..."}],
  "panes":[{"pane_id":"w2:p2","agent":"claude","agent_status":"idle","cwd":"...","focused":true,"tab_id":"w2:t1","workspace_id":"w2", "...":"..."}],
  "tabs":[{"tab_id":"w3:t1","workspace_id":"w3","label":"1","number":1,"pane_count":3,"agent_status":"done","focused":false}],
  "workspaces":[{"workspace_id":"w3","label":"port-manager","number":2,"pane_count":3,"tab_count":1,"active_tab_id":"w3:t1","agent_status":"done","focused":false}],
  "focused_pane_id":"w2:p2","focused_tab_id":"w2:t1","focused_workspace_id":"w2",
  "protocol":22,"version":"0.9.3"}}}
```

### Recommended ownership strategy

1. **Primary: env.** `HERDR_PANE_ID` etc. are inherited through `setsid` **[verified]**, so `devrun up` simply records them
   at start. No PID mapping needed. This is the answer to the SPEC's open question: *auto-fill `owner` from the
   environment of the process that ran `devrun up`.*
2. **Secondary (liveness of owner pane / human display):** check `.panes[].pane_id` in a snapshot or `herdr pane get <id>`
   to see whether the owner pane still exists. A recorded pane id missing from the snapshot means the owner is gone.
3. **If a PID→pane lookup is ever needed** (e.g. an `orphan` with no registry entry): walk `/proc/<pid>/stat` PPIDs until
   one equals a pane's `shell_pid` from `pane process-info --pane <id>` for each pane in `pane list`. Works only while the
   chain is unbroken. A `setsid` daemon whose parent exited is reparented to init and **breaks the chain**, so read its
   environ instead: `/proc/<pid>/environ` contains `HERDR_PANE_ID` **[verified]** (same-user readable). Prefer that to the walk.

## 4. Running outside herdr (`HERDR_ENV` unset)

Detect with `os.Getenv("HERDR_ENV") == "1"` and non-empty `HERDR_PANE_ID`. Three situations:

| Situation | Behavior |
|---|---|
| Inside a pane | env vars set, everything above works. |
| Outside herdr, **no server** | Any API command: exit 1, JSON `{"error":{"code":"server_not_running",...}}` on stdout **[verified]**. |
| Outside a pane, **server running** (other terminal, cron, plain ssh) | Commands still reach the default socket and succeed. See the hazard below. |

**Hazard [verified, contradicts the docs].** The docs say `--current` "requires a calling pane and errors when
`HERDR_PANE_ID` is unavailable." In 0.9.3 it did not error. With the env vars stripped it silently resolved to the
**focused pane of the whole session**, which was a different pane:

```
$ env -u HERDR_ENV -u HERDR_PANE_ID -u HERDR_TAB_ID -u HERDR_WORKSPACE_ID herdr pane current
{"id":"cli:pane:current","result":{"pane":{...,"focused":true,...,"pane_id":"w2:p2",...,"workspace_id":"w2"},"type":"pane_current"}}
$ echo $?
0
```

(We were really in `w3:p3`.) So devrun **must not** call `herdr pane current` / `--current` when `HERDR_PANE_ID` is
unset, or it will attribute the server to whichever pane the user is looking at. Rule:

- `HERDR_PANE_ID` set → owner from env (+ optional `agent get` lookup for name).
- unset → `owner = null`. No fallback to `pane current`. This replaces the SPEC's "or from `herdr pane current` when env
  vars are absent", which is unsafe.

Clean fallback plan:
- Resolve the binary as `$HERDR_BIN_PATH`, else `exec.LookPath("herdr")`, else herdr is absent.
- `notify`: if herdr absent, not in a pane, or the call returns `error` / `shown:false` → print the message and URL to
  stderr, still write `flagged` to the registry, **exit 0**. A missing notifier must not fail the agent's workflow.
- Every herdr call: short timeout (e.g. 2s), because a hung server shouldn't block `up`. Timeout behavior [unverified].
- Outside herdr but with a server running, `notify` can still work (default socket), but without a pane there's nothing to
  attribute. Allow it, owner null.
- `HERDR_SESSION` / `--session` select a named session socket [docs]; devrun should inherit the env and not override it.

## 5. JSON fields devrun should parse

All CLI output is an envelope: `{"id":"cli:<group>:<verb>","result":{...,"type":"<kind>"}}` on success or
`{"id":...,"error":{"code":"...","message":"..."}}` on failure, exit code 1. Check `.error` first.

| Purpose | Command | Field path | Notes |
|---|---|---|---|
| Error check (any call) | any | `.error.code`, `.error.message` | `server_not_running` is the one to special-case. |
| Owner pane | env | `HERDR_PANE_ID` | Prefer env over API. |
| Owner workspace / tab | env | `HERDR_WORKSPACE_ID`, `HERDR_TAB_ID` | |
| Owner agent kind | `pane current` (only when env set) | `.result.pane.agent` | e.g. `claude`. Absent on a pane with no agent [unverified]. |
| Owner agent name | `api snapshot` or `agent list` | `.result.snapshot.agents[] \| select(.pane_id==$P) \| .name` (`agent list`: `.result.agents[]`) | Optional key. Fall back to `.agent`. |
| Agent status | same | `.agent_status` | Seen: `idle`, `working`, `done`. Display only. |
| Agent session id | same | `.agent_session.value` | Optional. |
| Pane exists (owner alive) | `api snapshot` | `.result.snapshot.panes[].pane_id` | Or `pane get <id>` and test `.error`. |
| Pane cwd | `pane get` / snapshot | `.cwd`, `.foreground_cwd` | Could cross-check worktree. |
| Workspace label | `api snapshot` | `.result.snapshot.workspaces[] \| select(.workspace_id==$W) \| .label` | e.g. `port-manager`. |
| Shell / fg pgid | `pane process-info --pane P` | `.result.process_info.shell_pid`, `.foreground_process_group_id`, `.foreground_processes[].pid/.name/.argv/.cwd` | Only for the PPID-walk fallback. |
| Notify outcome | `notification show` | `.result.shown` (bool), `.result.reason` | `shown`, `disabled`, `rate_limited`, `no_foreground_client`, `busy` [docs]. |
| Server version | `api snapshot` | `.result.snapshot.version`, `.result.snapshot.protocol` | For `doctor`. Min tested: `0.9.3` / `22`. |

`doctor` checks: `HERDR_BIN_PATH` or `herdr` on PATH runs `--version`; `HERDR_ENV=1`; `herdr api snapshot` returns without
`.error` (server reachable); report `version`/`protocol`.

`devrun watch` launch line: `herdr pane split --direction right --cwd <path> ...` takes a `--cwd` and `--env`; the exact
syntax for passing a command (`-- devrun watch`) is **[unverified]**. I did not run `pane split` (forbidden). Check
`herdr pane split --help` and `pane run` before implementing; `pane run` ("Run a command in a pane") may be the intended way.

## Unverified / open

- `notification show` with no server running (inferred only, see §2).
- `notification show` result when `shown:false`, and its exit code.
- Whether the CLI times out against a hung (not absent) server.
- Exact `herdr pane split` syntax for running `devrun watch` in a new pane.
- `agent` field on `pane current` for a pane with no agent.
- The `--current`-outside-a-pane behavior may differ in other herdr versions. Pin the check to `HERDR_PANE_ID` instead
  of relying on herdr's own error.
- Only tested single local session; named sessions (`--session`) and `--machine` remote routing were not tried.

## Side effects of this research

One notification was shown (`"devrun test"`, `--sound none`). Nothing was created, closed, moved, or sent to another pane.
A short-lived `sleep 20` was spawned under `setsid` for the process-info test.
