# devrun agent guide

Paste the block below into a project's `CLAUDE.md` or `AGENTS.md`. The same text ships as a Claude Code skill in `skills/devrun/SKILL.md`; to use it, copy it to `~/.claude/skills/devrun/`:

```bash
mkdir -p ~/.claude/skills/devrun && cp skills/devrun/SKILL.md ~/.claude/skills/devrun/
```

## Snippet

```markdown
## Dev servers: use devrun

Many agents share this machine, each on its own git worktree. Ports are allocated by `devrun`; never pick one yourself.

- **Never hardcode a port** (no `3000`, `8080`, `--port 5173`). Start servers with `devrun up -- <cmd>`. devrun sets `PORT` and replaces `{port}` in the command, e.g. `devrun up -- npm run dev` or `devrun up -- python3 -m http.server {port} --bind 127.0.0.1`. If the tool ignores `PORT`, pass the port explicitly with `{port}` (or `--port-env VAR` for another variable name).
- `devrun up` waits until the port accepts connections, prints the URL, and is idempotent: running it again prints the same URL. The port is stable for this worktree.
- **Get the URL with `devrun url`** (`devrun url --json` for scripts) and give it to the browser tool, e.g. Playwright MCP `browser_navigate` with the printed URL. Never guess it.
- Use `devrun logs [-f]` to read server output and `devrun ls` to see every server.
- **When a human should look at something**, run `devrun notify "what to look at"` and keep working. It returns immediately. Run `devrun clear` once it has been inspected.
- **Before finishing, run `devrun stop`** unless the user told you to leave the server running. From another directory, or when several servers exist, pass a target: a name, a port, or `project/name` (`devrun stop frontend`, `devrun stop 41237`). An ambiguous target is refused with a candidate list and nothing is stopped; use the port or `project/name`.
- **Docker compose projects**: start with `devrun up --compose --ports web` (several: `--ports web,api`; custom variable: `--port-var web=FRONTEND_PORT`; rebuild: `--build`; non-default file: `-f compose.dev.yml`). The compose file must publish ports through env vars, `ports: ["${WEB_PORT}:3000"]`, with `API_PORT` etc. for other names, and must **not** set `container_name:` (it collides across worktrees). Stop with `devrun stop`; volumes are never removed.
- **If a port is taken or the server will not start**: do not kill whatever holds the port and do not switch to another number. Run `devrun ls` (is it your own server? then use its URL), `devrun logs` (did the app crash or ignore `PORT`?), and `devrun gc` (drops dead entries and reports orphans). Then re-run `devrun up`. devrun only allocates from 41000-41999 and skips ports that are busy. If a framework binds elsewhere, `up` warns about the drift; fix the command so it honours `PORT`/`{port}`.
- Bind to `127.0.0.1`; devrun does not change the interface your app listens on.
```

## Playwright

devrun does not require or install Playwright. If a Playwright MCP is available in your session, give it the output of `devrun url`:

```bash
devrun url      # http://127.0.0.1:41237  -> pass to browser_navigate
```

From a script, use `devrun url --json` and read the `url` field.

## Notes for humans

- Run `devrun watch` in a herdr pane to see all servers; flagged servers are highlighted.
- `devrun doctor` checks herdr, the state dir and the port range.
- Compose projects that hardcode ports cannot be remapped yet (`--override` is deferred); change the compose file to use `${WEB_PORT:-3000}`-style variables.
