# AGENTS.md — plugin-fleet

Standalone plugin repo owning the externalized `charly deploy` command
(`command:deploy`, compiled-in). The plugin is a Go module at
`candy/plugin-fleet/` (module path
`github.com/opencharly/plugin-fleet/candy/plugin-fleet`); the root `charly.yml`
only declares `discover: candy` so the repo is a project and its candy is
scanned.

Canonical files:

- `candy/plugin-fleet/charly.yml` — the `plugin-fleet:` candy entity (`plugin:`
  block, `plan:` check).
- `candy/plugin-fleet/plugin.go` / `command.go` — `NewProvider()` / `NewMeta()` /
  `CliMain` and the `Invoke(OpRun)` dispatch.
- `candy/plugin-fleet/walk.go` / `del_resolve.go` / `deploy_target.go` — the
  deploy-tree walk, the del resolution, and the deploy dispatch.
- `candy/plugin-fleet/from_box_pod.go` / `from_box_vm.go` — the `from-box` legs.
- `candy/plugin-fleet/schema/deploy.cue` — the self-contained plugin schema.
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.
- `README.md` — user overview only; never agent guidance.

## Load these skills first (R0)

- `/charly-core:deploy` — `charly deploy add/del`, pod or container deploys
  (the command's user-facing reference).
- `/charly-internals:plugin` — the plugin authoring reference: the `plugin:`
  block, the `command` provider class, the per-plugin CUE-schema contract.
- `/charly-internals:install-plan` — the InstallPlan IR the deploy dispatch
  compiles.
- `/charly-internals:git-workflow` — before any git/PR action.

## Build / validate / test

- `go build ./...` in `candy/plugin-fleet/` — compile the plugin module.
- `go test ./...` in `candy/plugin-fleet/` — the plugin's Go tests (the walk, del
  resolution, deploy-target, from-box, and schema-serve seams).
- `charly box validate` at the repo root — the structural check (the candy +
  `plugin:` block, CUE schema).
- The merge gate is the **org-wide** `charly/pr-validator` (required check
  `validate / validate`, defined in `opencharly/.github`); this repo has **no**
  per-repo candy gate.

## Modify this repo

- Edit the `plugin-fleet:` candy entity, the Go source, and `schema/deploy.cue`
  **together** — the schema is the served declaration surface.
- Keep the deploy handlers dispatching in-process via `Invoke(OpRun)`; the host
  seams (`deploy-add` / `deploy-del` / `deploy-config`) run the existing deploy
  orchestration verbatim. `from-box` and the del resolution run plugin-side.

## Landing

- PR-only. Every change lands through a pull request; the org-required
  `charly/pr-validator` validates the diff and body and arms native auto-merge on
  PASS. Direct pushes to `main` are blocked.
- History lives in `CHANGELOG/` (written by `tag-on-merge` at merge time); the PR
  body IS the changelog.
- The authoritative rulebook is the umbrella `AGENTS.md` in
  `opencharly/opencharly` and `charly/AGENTS.md` in the charly repo. Do not
  restate its rules here.
