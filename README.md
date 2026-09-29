# plugin-fleet

The `charly deploy` deployment CLI, externalized into a compiled-in command
plugin (`command:deploy`) — the deployment orchestration surface mirroring
`candy/plugin-vm`.

## What it provides

| Capability | Surface |
|---|---|
| `command:deploy` | the `charly deploy` CLI — `add` / `del` / `show` / `export` / `import` / `reset` / `path` / `status` / `from-box` |

The plugin is **compiled-in**: its leaves dispatch in-process via `Invoke(OpRun)`
(`runDeployCommand` → kong-parse the `DeployCmd` tree), so the handlers run in
charly's own process and inherit its real stdio/TTY natively — keeping `charly
deploy add`'s interactive prompts and dry-run output working.

`add` / `del` drive the whole deploy-tree walk plugin-side; the registry-backed
executor-chain derivation and the config-management leaves forward to the host
over four generic host-build seams where the host runs the existing deploy
orchestration verbatim (the config loader + deploy ledger, the InstallPlan
compiler, and the deploy-dispatch kernel stay core). `path` resolves plugin-side
via `kit.DefaultDeployConfigPath` (no seam).

## How to use it

```bash
charly deploy add <box> [name]   # deploy a box (interactive prompts preserved)
charly deploy del <name>         # tear a deployment down
charly deploy status             # per-deployment status
charly deploy from-box <box>     # materialize a deploy spec from a box
```

## Layout

- `candy/plugin-fleet/` — the plugin module: `plugin.go` (provider + meta),
  `command.go` (the CLI dispatch), `walk.go` (the deploy-tree walk),
  `del_resolve.go`, `deploy_target.go`, `deploy_cmd.go`, `from_box_*.go`,
  `schema/deploy.cue`, `cmd/serve/main.go`.
- `charly.yml` — the root project manifest (`discover: candy`).
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.

## Related

- Owning skill: `/charly-core:deploy` — `charly deploy add/del`, pod or container
  deploys. This candy carries no `skill:` entity of its own; the gap is tracked in
  [opencharly/opencharly#291](https://github.com/opencharly/opencharly/issues/291).
- `/charly-internals:install-plan` — the InstallPlan IR the deploy dispatch
  compiles.
- [`opencharly/charly`](https://github.com/opencharly/charly) — the charly CLI.
