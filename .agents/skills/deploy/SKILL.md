---
name: deploy
description: |-
  `charly deploy add`/`charly deploy del`, quadlet generation, volume backing, tunnels (Tailscale/Cloudflare), `add_candy:` overlay, or per-machine deploy overlays.
---

# Deploy - Deployment Configuration

## Schema overview

- **A deploy is name-first; the target is the SUBSTRATE KIND at the edge** — the deploy's first child key is exactly one of `pod:` / `vm:` / `kubernetes:` / `local:` / `android:` (or `group:` for a targetless group of members) and selects `local | vm | pod | kubernetes | android`. There is NO authored `target:` field. Inside that substrate node, `image:` names the box/OCI image a `pod:`/`kubernetes:`/`android:` runs and `from:` inherits a same-kind `vm`/`kubernetes`/`local`/`android` template by name.
- **The substrate node at the edge** — `vm: {from: <entity>}` for a VM target, `pod: {image: <name>}` for a pod target, `kubernetes: {from: <name>}` for a cluster target, `local: {from: <template>}` for a local-deploy. Deploy-into nesting is **tree position** (a resource node placed under another resource), not a `nested:` field; sibling members (a resource node placed under the deploy) replace the old `peer:` field.
- **`host:` is a FIELD on `local:`, never a deploy KIND (MUST).** A host/remote deploy MUST be authored as `local: {from: <template>, host: <user@machine>}` — the `host:` scalar on a `local:` (or `pod:`) deploy: `host: local` / absent = this machine, `host: <user@machine>` = SSH. There is NO standalone `host:` venue KIND; authoring a `host:` node is a hard load error. See `/charly-local:local-deploy`.
- **`group:` is EXCLUSIVELY a targetless deploy group.** A `group:` node carries only resource members (pod/vm/etc.) with no own workload — the targetless deploy group.
- **Disposability is a deploy property** — `DeployNode.Disposable` (the `disposable:` scalar under the substrate node) is the sole source of truth.
- **Resource arbitration is a deploy property** — the `preemptible:` field (holder; occupies exclusive host-resource token(s), may be gracefully stopped + restored), the `requires_exclusive:` field (claimant; needs SOLE use), and the `requires_shared:` field (claimant; REFCOUNTED shared use — many concurrent claimants; a GPU-token claim flips the whole IOMMU group vfio→nvidia+CDI at deploy, back at teardown when the LAST claimant releases) — all inline in the substrate node — drive the arbiter (`charly preempt`). A fourth axis ORTHOGONAL to disposable/ephemeral/lifecycle. See "Preemptible resource arbitration" below + `/charly-internals:disposable`.
- **Disposable R10 test beds are just `disposable: true` deploys** (run via `charly check run <bed>`) — there is NO separate bed kind. The main repo's beds (`check-pod`, `check-k3s-vm`, `check-sway-browser-vnc-pod`, …) AND every `box/<distro>` submodule's beds (the arch / cachyos / debian / ubuntu / fedora bootstrap-VM + pacstrap/debootstrap beds, in each submodule's `charly.yml`) are disposable deploys. The cachyos submodule's `charly-cachyos` operator workstation profile is a non-disposable deploy (a profile, not a test bed). Operator deployments otherwise live in the per-host `~/.config/charly/charly.yml`. See `/charly-check:check` "disposable check deploys".

## Overview

`charly deploy` is the parent verb for applying and tearing down deployments, plus managing `charly.yml` overrides. The command family has two distinct surfaces:

1. **Execution verbs** — `charly deploy add <name>` / `charly deploy del <name>`. Apply or reverse a deployment. The target is dispatched by which substrate node the deploy carries:
   - `local: {from: <template>}` → the external `deploy:local` plugin (`candy/plugin-deploy-local`) applies to the local filesystem over the executor reverse channel (or, placed under another resource node via tree position, via NestedExecutor into the enclosing deployment). See `/charly-local:local-deploy`.
   - `vm: {from: <entity>}` → the external `deploy:vm` plugin (`candy/plugin-deploy-vm`) applies INSIDE a running VM over SSH via the reverse channel; the plugin auto-boots the venue in its `OpPrepareVenue` (via `HostBuild("cli")`), using the host prepare hook's resolved data. See "VM target" section below and `/charly-internals:vm-deploy-target`.
   - `pod: {image: <image>}` → the candy `plugin-deploy-pod` renders the overlay Containerfile (in its own code via `deploykit.OCITarget` + `deploykit.NewRenderGeneratorFromProject`) + the quadlet config-write (fully plugin-side in `candy/plugin-deploy-pod` + `sdk/deploykit/quadlet_paths.go` — the former core quadlet path is DELETED, K-wave 2).
   - `kubernetes: {from: <name>}` → Kustomize base/overlays tree. See `/charly-kubernetes:kubernetes`.
2. **Config-file management** — `charly deploy show/export/import/reset/path/status`. Read and mutate `~/.config/charly/charly.yml` itself.

## Targets, one schema

The same `DeployNode` shape feeds every target (`pod`, `local`, `vm`, `kubernetes`, `android`) — authors describe *what the workload needs* (ports, volumes, env, security, tests); the generator per target decides *how Kubernetes / quadlet / local apply / VM over SSH / Android apk-install* realizes it.

A deploy's install timeline can include a `run:` step carrying a plugin verb (the `<word>: <input>` sugar — see `/charly-image:layer` "Plugin verb steps"). When the verb is served by an EXTERNAL (out-of-process) plugin, that step EXECUTES at deploy on a `local:`/`vm:` target — the plugin runs its effect on the live venue over the executor reverse channel (`Invoke(OpExecute)`) and returns teardown `ReverseOp`s the target records to the ledger and replays at `charly deploy del`. A builtin `ProvisionActor` verb keeps its in-proc shell path; the two are placement-agnostic (same authored step, the runtime picks the path). Detail → `/charly-internals:install-plan` (the `ExternalPluginStep` IR kind) + `/charly-internals:plugin`.

**An `android:` deploy** installs a deploy's `add_candy:` candies' `apk:` packages
onto an `android` device entity (an in-pod emulator or a remote/physical adb
endpoint) via the EXTERNAL `deploy:android` substrate (F1 — served out-of-process
by candy/plugin-adb; the host preresolves the device endpoint + apk specs, the
plugin installs) — the Android analogue of a `kubernetes:` deploy emitting workloads onto
a cluster. The substrate node is `android: {from: <device>}`; apps
ride in on the deploy's inline `add_candy:` list (no apk-list field). A `pod → android` tree
(the device placed under its emulator-pod node) mirrors `vm → kubernetes`; a pod's android
children deploy AFTER `charly start` (use `--node-only` on the pod's deploy-add, then
dotted-path `charly deploy add pod.device`). See `/charly-check:android`. Kubernetes-specific choices (storage class, ingress class, cert issuer, secret backend) live in a **cluster profile** file (`~/.config/charly/clusters/<name>.yaml` or in-repo `clusters/<name>.yaml`), *not* in the deployment. This means one deployment spec targets dev/staging/prod clusters with zero schema changes — only the cluster profile differs.

`charly start` / `charly stop` remain as ergonomic wrappers: `charly start <image>` is equivalent to `charly deploy add <image> <image>` with the container target; `charly stop <name>` is `charly deploy del <name>`. New scripts should prefer the explicit `charly deploy add`/`charly deploy del` forms, especially when using `--add-candy` overlays or the `host` target.

## Quick Reference

| Action | Command | Description |
|--------|---------|-------------|
| Apply container deploy | `charly deploy add <name> <ref>` | Compile candies + build overlay if `add_candy:` present + run via quadlet |
| Apply local deploy | `charly deploy add <name> <ref>` (entry with a `local:` substrate node) | Apply layers directly to local filesystem (see `/charly-local:local-deploy`) |
| Tear down deploy | `charly deploy del <name>` | Stop container + reverse ReverseOps (host) + ledger cleanup |
| Dry-run | `charly deploy add <name> <ref> --dry-run [--format=json]` | Print the InstallPlan without executing |
| Layer overlay | `charly deploy add <name> <ref> --add-candy <ref>` | Extra layer(s) applied on top; repeatable |
| Configure deployment | `charly config <image>` | Generate .container file + save charly.yml |
| Configure instance | `charly config <image> -i <instance>` | Generate instance-specific quadlet + deploy entry |
| Configure volume backing | `charly config <image> --bind name` | Set volume as host bind mount |
| Provision data | `charly config <image> --seed` | Auto-provision data candies into bind mounts (default) |
| Deploy status | `charly deploy status` | Audit charly.yml vs quadlet sync |
| Show overrides | `charly deploy show [image]` | Display charly.yml contents |
| Show instance overrides | `charly deploy show <image> -i <instance>` | Display instance-specific overrides |
| Import config | `charly deploy import <files>` | Merge files into charly.yml |
| Reset config | `charly deploy reset [image]` | Remove charly.yml overrides |
| Reset instance config | `charly deploy reset <image> -i <instance>` | Remove instance overrides |
| Push to registry | `charly box build --push` | Multi-platform push |

For service lifecycle commands (start/stop/status/logs/update/remove), see `/charly-core:service`. For VM lifecycle (build/create/start/stop/ssh), see `/charly-vm:vm`; for in-VM layer deploys via `charly deploy add vm:<name>`, see the "VM target" section below and `/charly-internals:vm-deploy-target`. For encrypted storage, see `/charly-automation:enc`. For host-target semantics, see `/charly-local:local-deploy`. For Kubernetes targets, see `/charly-kubernetes:kubernetes`. For the Go IR that drives all five targets, see `/charly-internals:install-plan`.

## Command Family: `add` / `del`

### `charly deploy add <name> [<ref>]`

Applies a deployment. The substrate node selects the target:

- **`local: {from: <template>}`** — apply layers to the local filesystem via the external `deploy:local` plugin (`candy/plugin-deploy-local`) over the executor reverse channel. With `host: local` (default) the apply runs through a `ShellExecutor` directly; with `host: <user@machine>` it runs over an `SSHExecutor` (picked by `rootExecutorForDeployNode`). See `/charly-local:local-deploy`.
- **`vm: {from: <entity>}`** — apply layers inside a running `vm` entity via SSH (the external `deploy:vm` plugin, `candy/plugin-deploy-vm`, over the reverse channel). `<vm-name>` must match a `vm` entity; the VM must already be created (`charly vm create <vm-name>`), or the plugin auto-boots it in its `OpPrepareVenue` (via `HostBuild("cli")`). See "VM target" section below.
- **`kubernetes: {from: <name>}`** — emit a Kustomize base/overlays tree. See `/charly-kubernetes:kubernetes`.
- **`pod: {image: <image>}`** (pod, the default target) — container deployment. Multiple pod deploys coexist (`my-dev`, `postgres-staging`, etc.); each gets its own quadlet, container name, and charly.yml entry.

`<ref>` accepts four forms, auto-detected:

| Form | Example | Resolution |
|---|---|---|
| Local image name | `fedora-coder` | Looked up in current project's `charly.yml` |
| Local layer name | `pre-commit` | Looked up in current project's `candy/` directory |
| Local YAML path | `./custom.yml`, `/abs/path/charly.yml` | File's top-level keys classify image vs layer |
| Remote repo | `github.com/owner/repo[/box/<n>\|/candy/<n>][@ref]` | Fetched via existing `--repo` cache |

Disambiguation: a ref containing `/candy/` resolves to a candy; `/box/` to a box. For local names, `box/` is checked before `candy/`; same-named entries in both are a hard error. The legacy `@host/org/repo:version` form (used by `require:` and `candy:` in charly.yml) is also accepted.

When `<ref>` is omitted, the ref falls back to the deploy's `image:` field (or the deploy key itself if no explicit image is declared).

### `charly deploy add` flags

**Universal:**
- `--tag <calver>` — override charly.yml tag
- `--dry-run` — print the InstallPlan without executing
- `--format table|json` — with `--dry-run`
- `--pull` — force re-fetch of remote refs / image pull
- `--verify` — run layer `check:` post-deploy
- `--add-candy <ref>` — repeatable; extra layer(s) applied on top (all 4 ref forms)

**Per-deploy selector overrides** — the deploy-entity level of the resolution chain
`flag > deploy entity > env > settings file > built-in default`. Each value is validated
against the schema-owned vocabulary, applied to the deploy for this run, and persisted onto
the deploy entry so the next load resolves the same value:
- `--engine <word>` — container engine for a `pod:` deploy (`auto`, or a member of the spec
  engine vocabulary). A composed candy whose `engine:` constraint disagrees is an error,
  never an override.
- `--backend <word>` — VM backend for a `vm:` deploy (`auto`, or a member of the spec
  VM-backend vocabulary; `krun` is explicit-only and never auto-detected).
- `--namespace <name>` — containerd namespace for a `pod:` deploy. A per-deploy value
  overrides `containerd.namespace` in the settings file, which overrides the engine
  capability's default.
- `--rootful <mode>` — rootful-launch mode for a `pod:` deploy (a member of the spec
  rootful vocabulary).

**Local-target-specific** (silently ignored on pod deploys):
- `--with-services` — opt-in for systemd unit installation (packaged-unit enable + drop-ins)
- `--allow-repo-changes` — opt-in for repo config mutations (rpmfusion, copr, external `.repo` files)
- `--allow-root-tasks` — opt-in for arbitrary `cmd: user: root` task bodies
- `--skip-incompatible` — skip layers lacking a host-matching format section instead of failing
- `--builder-image <ref>` — override the compile-builder image
- `--assume-yes` / `-y` — all three gates plus skip sudo preflight

### `charly deploy del <name>`

Reverses a deployment. Gated host-side reversal respects `--keep-repo-changes` and `--keep-services`. Container teardown: `podman stop` + `rm` + overlay image removal (unless `--keep-image`) + ledger cleanup. VM teardown: SSH-executed ReverseOps in the guest, preserving the VM itself (use `charly vm destroy` separately). See `/charly-local:local-deploy` for the full 15-kind ReverseOp table.

## VM target: `charly deploy add vm:<vm-name> <ref>`

Applies layer recipes **inside a running VM** over SSH. Same `InstallPlan` IR as host and container targets; the difference is that bash bodies run via `ssh guest 'sudo bash -s'` through an `SSHExecutor` (see `/charly-internals:vm-deploy-target`).

**Prerequisite**: the VM must exist before `charly deploy add vm:...` runs. `charly deploy add vm:<name>` does NOT auto-provision; a missing VM produces a clean error pointing at `charly vm create`:

```bash
charly vm create arch                            # provision VM first
charly deploy add vm:arch ripgrep                # then apply layer in guest
charly deploy add vm:arch fedora-coder \
    --add-candy team-extras \
    --add-candy github.com/team/configs/candy/sshkeys
charly deploy del vm:arch                        # reverse all applied layers (VM stays up)
```

### `VmDeployState` schema in charly.yml

When `charly deploy add vm:<name>` completes, a `vm_state:` block lands inline in the charly.yml deploy:

```yaml
arch:                                          # the deploy name; addressable as vm:arch
  vm:
    from: arch                                 # inherit the vm template (implies the VM target)
    add_candy:
      - ripgrep
      - github.com/team/configs/candy/sshkeys
    install_opts:
      verify: true
    vm_state:
      instance_id: 7b3a8f42-...                # stable UUID across rebuilds
      disk_path: /home/user/.local/share/charly/vms/arch.qcow2
      seed_iso: /home/user/.local/share/charly/vms/arch-seed.iso
      ssh_port: 2222
      ssh_user: arch
      backend: libvirt
```

`vm_state` is the persisted venue state: re-applies pick up `instance_id` (cloud-init uses it as a stable identifier) plus the disk/seed paths and SSH endpoint. SSH access uses the managed `charly-<vmname>` ssh-config alias written by `charly vm create`.

### Marking a deploy VM-targeted: the `vm:` substrate node

A deploy is VM-targeted when its first child key is the `vm:` substrate node carrying `from: <entity>` — there is no `target:` field. CLI dispatch additionally accepts a `vm:<vm-name>` prefix on the deploy-name argument (`charly deploy add vm:arch`), which `ResolveTarget` reads to route through `pluginDeployTarget` (S3b — the adapter for the external `vm` substrate, dispatched through `candy/plugin-fleet`'s `Invoke(OpDeployDispatch)`). The prefix is an addressing convenience; the `vm:` substrate node is the source of truth. Naming a `vm:` deploy whose `vm` entity isn't declared in `charly.yml` errors at `charly deploy add` time; a declared-but-unbooted VM is auto-booted by the vm deploy plugin's `OpPrepareVenue`.

### `add_candy:` overlay semantics for VM targets

When `charly deploy add vm:<name> <ref>` runs with `--add-candy`, the extra layers are applied **inside the guest** alongside the primary ref. The compiler merges `<ref>` + `add_candy:` into a single topo-sorted `InstallPlan`; the external `deploy:vm` plugin's `kit.WalkPlans` walks it inside the guest over SSH. The host-side ledger (keyed by `computeDeployID`) records both the base and overlay layers' teardown ops so `charly deploy del vm:<name>` reverses the full set.

This is the same merge semantics as a `local:` deploy — just with SSH-wrapped execution. See `/charly-internals:install-plan` for the compiler and `/charly-internals:vm-deploy-target` for the execution model.

### VM-relevant `install_opts:` fields

`install_opts:` in the charly.yml entry mirrors the CLI flags on `charly deploy add`. For VM targets, the relevant ones are:

| Field | Effect |
|---|---|
| `with_service` | Enable systemd units declared in layers' `service:` lists |
| `allow_repo_changes` | Permit repo-config mutations (rpmfusion, copr) in the guest |
| `allow_root_tasks` | Permit arbitrary `cmd: user: root` tasks in the guest |
| `verify` | Run layer `check:` over SSH post-deploy |
| `skip_incompatible` | Skip layers lacking a guest-matching format section |

`builder_image` + `yes` also apply. Host-target-only gates that don't apply to VM target: none — the gate semantics are identical.

### Prerequisite chain

```
charly.yml declares a vm entity (`<name>: {vm: …}`)
    → charly vm build <name>         (cloud_image: fetch+resize+seed ISO; bootc: install to-disk)
    → charly vm create <name>        (libvirt domain + SMBIOS ssh key + passt portForward)
    → charly vm start <name>         (boot)
    → charly deploy add vm:<name> <ref>  (SSH → cloud-init wait → charly install → layer apply)
    → charly deploy del vm:<name>    (SSH → ReverseOps; VM stays up)
    → charly vm destroy <name>       (remove libvirt domain; --disk to also delete qcow2)
```

See `/charly-vm:vms-catalog` for vm-entity authoring, `/charly-vm:vm` for the lifecycle commands, `/charly-internals:vm-deploy-target` for the Emit flow.

### `add_candy:` overlay mechanism

Both container and host targets accept extra candies at deploy time via `--add-candy <ref>` (repeatable) or the deploy's inline `add_candy:` list. Semantics:

- **Container target**: synthesizes an overlay Containerfile (`FROM <base-image>` + the extra candies' build steps) and builds a deterministic overlay image tagged `<deploy-name>-overlay:<short-hash>`. The deploy runs the overlay, not the base image. Re-running with different overlays rebuilds. `charly deploy del <name>` removes the overlay unless `--keep-image`.
- **Host target**: the compiler merges the box's candies with `add_candy:`, topo-sorts the union, and compiles one `InstallPlan` covering the combined set. The ledger records which candies (base + overlay) were applied so teardown reverses everything.

Ref forms for `--add-candy` are identical to the primary `<ref>` positional (local name / local path / remote / legacy `@` form).

## Two supported deploy patterns

Every pod deploy MUST declare its `image:` field
explicitly (a pod deploy is defined by carrying `image:`; hard
load-time error if absent — see "Why `image:` is required" below).
With that contract locked, two distinct patterns are supported:

### Pattern A — Multiple instances of the same image

Use the `<base>/<instance>` deploy-name form when you want N
container instances backed by the same image (one per tenant,
per workspace, per environment, …):

```yaml
versa:                             # base instance — name == image (just convention)
  pod:
    image: versa

versa/ecovoyage:                   # `<base>/<instance>` deploy name
  pod:
    image: versa                   # SAME image; explicit (no inference)
    volume:
      - name: workspace
        type: bind
        host: /home/user/projects/ecovoyage
    port:
      - "32718:2718"               # explicit pinned ports; other ports auto-allocate

versa/another-tenant:              # second instance of versa
  pod:
    image: versa
    volume:
      - name: workspace
        type: bind
        host: /srv/another-tenant/workspace
```

Container names: `charly-versa`, `charly-versa-ecovoyage`,
`charly-versa-another-tenant`. Equivalent CLI invocations:
`charly update versa -i ecovoyage` ↔ `charly update versa/ecovoyage` (the
`-i <inst>` flag and the `/` suffix are interchangeable for
addressing instance deploys).

### Pattern B — Arbitrary deploy name with image (and optional version pin)

The deploy key is purely a name; it does NOT have to match the
`image:` value. Use this for version pinning, canary deploys, or
when you want a more descriptive deploy name than the image name
itself:

```yaml
versa-prod:                                   # arbitrary deploy name
  pod:
    image: versa                              # short name → resolves to current build

versa-pinned-2026.131.2134:                   # pinned-version deploy
  pod:
    image: ghcr.io/opencharly/versa:2026.131.2134  # explicit ref, never re-resolved

versa-canary:                                 # canary deploy on a rolling tag
  pod:
    image: ghcr.io/opencharly/versa:next
```

Container names: `charly-versa-prod`, `charly-versa-pinned-2026.131.2134`,
`charly-versa-canary`. Only the single `<key>` form addresses these
(`charly update versa-prod`, NOT `charly update versa -i prod` — that would
target an instance of `versa`, which is a different deploy).

### Schema rules locked down by these patterns

- **`image:` is REQUIRED on every pod deploy.** Hard
  load-time error if absent, with a remediation hint pointing at
  `charly migrate` (the one-shot migration that injects
  the field into legacy entries).
- **The `image:` value is either**:
  - a **short name** (e.g. `versa`) — resolved against the `candy:`
    image entries (those carrying `base:`/`from:`) in `charly.yml` to the currently-built tag, OR
  - a **fully-qualified registry ref** (e.g.
    `ghcr.io/opencharly/versa:2026.131.2134` or
    `…@sha256:…`) — pinned to that exact image, never re-resolved.
    Use this for version pinning and canary deploys.
- **The deploy key is purely a name.** It may contain `/` to express
  the multi-instance pattern (`<base>/<instance>`) but does NOT have
  to match `image:`. `versa-old`, `versa-canary`, and
  `my-tenant/staging` are all valid deploy keys.
- **Container name rule**: `charly-<key-with-slash-replaced-by-dash>`
  (e.g. `charly-versa`, `charly-versa-ecovoyage`, `charly-versa-canary`).
- **`charly update <key>` and `charly update <base> -i <inst>`** are
  equivalent ways to address a `<base>/<inst>` deploy (Pattern A).
  For Pattern B (arbitrary name), only the single `<key>` form
  works.

### Auto port mapping — inherited container ports, auto-allocated on 127.0.0.1

A box's published CONTAINER ports are inherited from its candy chain (boxes
declare no `port:` — see `/charly-image:image` "Ports"). At `charly config`,
`ResolveDeployPorts` maps EACH inherited container port to a host:container
publish entry: a fresh free **`127.0.0.1`** host port is auto-allocated unless
the deploy explicitly **pins** it. The result persists as `resolved_port:` and a
prior allocation is reused for stability across `charly update`. `localizePort` +
`BindAddress` (default `127.0.0.1`) bind every published port — auto-allocated
and pinned alike — to loopback only; `charly status` shows the live mapping and
check probes resolve it via `${HOST_PORT:N}`.

### Peer-to-peer between MEMBERS — how one member reaches another

Two mechanisms exist and they fail in different ways. Choose deliberately.

**1. By member name** (`charly-<bed>-<member>`) — resolved by `aardvark-dns`
inside the venue, i.e. an ordinary `getent hosts` / NSS lookup from within the
container. Readable and portable across hosts, and the right default when a peer
must be addressed by name (some CLIs reject a bare hostname and need the address:
resolve it in the venue and substitute the literal IP).

**2. By resolved host port** — every published container port is auto-mapped to a
free `127.0.0.1` host port (see above) and reachable from check probes as
`${HOST_PORT:N}`. No name resolution is involved at all.

**The failure mode you will actually hit.** Member-name resolution depends on
`aardvark-dns` running in the network namespace podman is CURRENTLY using. podman
tears the rootless netns down when the container count reaches zero and creates a
fresh one on the next start — but it does NOT restart aardvark, which keeps
serving the dead namespace. Then:

- the aardvark process is alive, so nothing looks broken
- the bridge exists in the new namespace with the right gateway
- the zone file is correct and lists the new containers by name and IP
- `podman network inspect` still reports `dns_enabled=true`
- and every `getent hosts <peer>` in every container returns EMPTY

A disposable bed causes this routinely, because its teardown removes the last
containers on an idle host. The breakage therefore appears in the NEXT run, after
a green one — so the change under test and the bed that just passed both look
guilty, and neither is.

Diagnose it by comparing namespaces, not by reading logs:

```bash
readlink /proc/$(pgrep -x aardvark-dns)/ns/net        # where aardvark serves
podman unshare --rootless-netns readlink /proc/self/ns/net   # where podman is
```

Different values mean container-name DNS is dead HOST-WIDE, for every bed and
every session, not just yours. `charly doctor` reports this as
`aardvark-dns namespace  STRANDED` (opencharly/plugin-doctor). Recovery needs the
stale pidfile removed as well, or podman believes a healthy daemon is running:

```bash
kill -9 <pid>
rm -f ${XDG_RUNTIME_DIR}/containers/networks/aardvark-dns/aardvark.pid
podman network reload --all
```

**Guidance.** Prefer `${HOST_PORT:N}` when a step only needs to REACH a peer — it
has no DNS dependency and therefore cannot fail this way. Use the member name when
the peer must genuinely be addressed by name, and expect that a bed doing so will
fail on a host whose aardvark has been stranded by an earlier teardown.

A deploy/bed `port:` entry is a **PIN** (`host:container`) for specific container
ports — the rest still auto-allocate (it is no longer a wholesale replacement,
and the legacy `port: [auto]` sentinel is retired — absence IS auto). Pin only
when you need a fixed, predictable host port:

```yaml
jupyter:
  pod:
    image: jupyter
    port: ["8888:8888"]   # pin 8888 for a stable bookmark; other ports auto-allocate
```

**Port resolution is keyed by deploy-key, not image short-name.** Each deploy
entry's quadlet `PublishPort=` is sourced from THAT entry's own
`resolved_port:`/`port:`, looked up by its deploy key. Multiple deploys backed by
the same image short-name on one host (Pattern A instances, Pattern B
pinned/canary deploys, and disposable check deploys whose `image:` matches a running
production deploy) each get their own independent ports — a bed's
auto-allocation never collides with a sibling's (the allocator excludes other
deploys' host ports). `MergeDeployOntoMetadata` and `dc.Lookup` both take the
deploy key (typically `c.Image`), never a value derived from the baked
`ai.opencharly.box` label, so an entry's resolved ports are never clobbered by a
sibling that merely shares the image.

### Why `image:` is required (R10 implication)

The check runner inspects exactly the image the operator declared in
`image:`, never what the container happens to be. Without an explicit
`image:` field the runner would resolve the running container's image
ref via `containerImageRef()`; for volume-pinned deploys (where
`data_source:` seeds workspace from a specific image tag) the running
container sits at the seed-version, not the current image, so the
runner would read the older OCI label and silently drop any probes
added since. Requiring `image:` makes the inspected image deterministic.

## Examples

**Deploy a local image as a container:**
```bash
charly deploy add my-dev fedora-coder
# Uses the my-dev deploy's inline volume/port/env/tunnel config.
charly deploy del my-dev
```

**Deploy directly to the host:**
```bash
charly deploy add host fedora-coder --with-services --assume-yes
charly deploy del host --assume-yes
```

**Deploy from a remote repo:**
```bash
charly deploy add my-coder github.com/opencharly/charly/box/fedora-coder@main
charly deploy add host github.com/team-acme/private-configs/candy/my-team-tools
```

**Add overlay layers:**
```bash
charly deploy add host fedora-coder \
    --add-candy team-extras \
    --add-candy github.com/team/configs/candy/sshkeys \
    --add-candy ./private.yml \
    --with-services
```

**Dry-run to preview the plan:**
```bash
charly deploy add host fedora-coder --dry-run --format=json
```

**`charly start`/`charly stop` equivalence:**
```bash
charly start fedora-coder            # == charly deploy add fedora-coder fedora-coder (container target)
charly stop fedora-coder             # == charly deploy del fedora-coder
```

## Quadlet Generation

User-level systemd services via podman quadlet. Generated by `charly config`.

### Generated File

Path: `~/.config/containers/systemd/charly-<image>.container` (or `charly-<image>-<instance>.container` with `-i`).

Contents include:
- `[Container]` section: image reference, container name, port mappings, volumes, environment
- `[Service]` section: restart policy, lifecycle hooks
- `[Install]` section: `WantedBy=default.target` (omitted only when encrypted volumes are backed by the `config` secret backend)
- `PodmanArgs=` for security settings (privileged, capabilities, devices)
- `Volume=` for named volumes and plain bind mounts
- `Environment=` / `EnvironmentFile=` for env vars
- `ExecStartPost=` / `ExecStopPost=` for tunnel commands

Service name: `charly-<image>.service`. Container name: `charly-<image>`. Entrypoint: determined by the embedded `init:` vocabulary for the configured init system. Encrypted volumes are mounted via `ExecStartPre=charly config mount` in the quadlet, which creates transient `charly-enc-<image>-<volume>.scope` units for each encrypted volume. These scope units are independent of the container service — they survive stop/restart (see `/charly-automation:enc`). The boot gate is the **secret backend**: with a keyring-class backend (`keyring`, `auto`, or unset) the deploy starts at boot, and `ExecStartPre` blocks in the mount until the keyring unlocks (`TimeoutStartSec=0`, so it waits indefinitely rather than failing); with `secret_backend: config` there is no `WantedBy=default.target` and the deploy needs an explicit `charly start` — a security decision recorded at sdk#121, because a passphrase stored in cleartext must not be auto-mounted, unattended, at every boot.

### Security in Quadlet

Layer and image-level security settings become `PodmanArgs=` in the quadlet file:

- `privileged: true` -> `PodmanArgs=--privileged`
- `cap_add` -> `PodmanArgs=--cap-add=<CAP>`
- `devices` -> `PodmanArgs=--device=<DEV>`
- `security_opt` -> `PodmanArgs=--security-opt=<OPT>`

Source: `sdk/deploykit/security.go`, `sdk/deploykit/quadlet.go`.

### Image Transfer

When `engine.run=docker`, `charly config` auto-detects if the image is missing from podman and transfers via `docker save | podman load`. `charly update` re-transfers if needed.

Source: `sdk/deploykit/quadlet.go` + `sdk/deploykit/quadlet_pod.go` (generation), `charly/commands.go` (command structs).

## Tunnel Configuration

Expose services outside the container host via tunnels. Tunnel config lives exclusively in `charly.yml` — it is NOT in `charly.yml` or OCI image labels. `charly config setup` persists tunnel config automatically via `saveDeployState`.

### Tailscale Serve (tailnet-private, default)

Exposes a port to your Tailscale network only. No FQDN needed -- Tailscale handles TLS automatically. Any port works for tailnet-only serve.

```yaml
tunnel: tailscale
# or expanded:
tunnel:
  provider: tailscale
  private: all       # all image ports on tailnet
```

**`bind_address` must be `127.0.0.1` (the default).** Setting `0.0.0.0` causes the container to bind on the Tailscale interface, preventing Tailscale from intercepting TLS. Result: HTTPS fails with `wrong version number`.

**Port form in `charly.yml`.** The canonical form is bare `H:C` (e.g. `8888:8888`); `charly config` prepends `127.0.0.1:` automatically when a tunnel is set. The IP-prefixed form `127.0.0.1:8888:8888` (and IPv6 `[::1]:8888:8888`) is also accepted — the canonical `ParsePortMapping` helper in `spec/spec/port_mapping.go` normalizes both shapes to a single-prefixed `PublishPort=` line, so neither form produces a doubled `127.0.0.1:127.0.0.1:8888:8888` quadlet. Unparseable port strings are logged loudly to stderr rather than silently dropped (a silent skip would otherwise suppress the entire `ExecStartPost=tailscale serve` block when even one port couldn't be parsed).

### Tailscale Funnel (public internet)

Exposes a port to the public internet via Tailscale's edge network. Funnel restricts HTTPS ports to: 443, 8443, 10000.

```yaml
tunnel:
  provider: tailscale
  public: [8080]    # funnel ports must be 443, 8443, or 10000
```

### Cloudflare Tunnel

Routes traffic through Cloudflare's network. Requires the deploy's `dns:` hostname. Prerequisite: `cloudflared tunnel login` (one-time auth).

```yaml
tunnel:
  provider: cloudflare
  port: 3001
  tunnel: my-tunnel    # optional, defaults to charly-<image>
dns: "app.example.com"
```

`charly config` handles the full tunnel lifecycle automatically:
1. Creates the Cloudflare tunnel (`cloudflared tunnel create`) if it doesn't exist
2. Writes the tunnel config YAML (`~/.config/charly/tunnels/<name>.yml`) with ingress rules
3. Routes DNS with `--overwrite-dns` (creates or updates CNAME to tunnel)
4. Generates a companion systemd service (`charly-<image>-tunnel.service`)
5. Enables the tunnel service and adds `Wants=` to the container quadlet

`charly start` then starts both the container and the tunnel service together.

### Backend Schemes

Port protocols declared in `charly.yml` control the backend URL scheme used by tunnel commands. The protocol flows from layer → OCI label (`ai.opencharly.port_proto`) → tunnel command.

**Tailscale serve/funnel schemes:**

| Scheme | Target URL | Tailscale flag | Use case |
|--------|-----------|----------------|----------|
| `http` (default) | `http://127.0.0.1:PORT` | `--https` | Plain HTTP backends |
| `https` | `https://127.0.0.1:PORT` | `--https` | HTTPS with valid cert |
| `https+insecure` | `https+insecure://127.0.0.1:PORT` | `--https` | HTTPS with self-signed cert (e.g., Traefik) |
| `tcp` | `tcp://127.0.0.1:PORT` | `--tcp` | Raw TCP forwarding |
| `tls-terminated-tcp` | `tcp://127.0.0.1:PORT` | `--tls-terminated-tcp` | TLS-terminated TCP |

**Cloudflare tunnel schemes:**

| Scheme | Ingress service | Use case |
|--------|----------------|----------|
| `http` (default) | `http://localhost:PORT` | HTTP origins |
| `https` | `https://localhost:PORT` | HTTPS origins (use with `noTLSVerify`) |
| `tcp` | `tcp://localhost:PORT` | Raw TCP (requires client-side cloudflared) |
| `ssh` | `ssh://localhost:PORT` | SSH tunneling |
| `rdp` | `rdp://localhost:PORT` | RDP streaming |
| `smb` | `smb://localhost:PORT` | SMB/CIFS file sharing |

**UDP** ports are never tunneled — a warning is printed. UDP traffic works directly between tailnet nodes.

`charly box validate` checks port schemes against provider capabilities. For example, `ssh` is valid for Cloudflare but not Tailscale; `tls-terminated-tcp` is valid for Tailscale but not Cloudflare.

See `/charly-image:layer` for port protocol syntax in `charly.yml`.

### Multi-Port Tailscale Serve

When `private: all` or `ports: all`, every image port gets its own `tailscale serve` command with scheme-appropriate flags:

```yaml
tunnel:
  provider: tailscale
  private: all
```

- `tailscale serve --bg --https=PORT http://127.0.0.1:PORT` (http, default)
- `tailscale serve --bg --https=PORT https+insecure://127.0.0.1:PORT` (https+insecure)
- `tailscale serve --bg --tcp=PORT tcp://127.0.0.1:PORT` (tcp)
- `tailscale serve --bg --tls-terminated-tcp=PORT tcp://127.0.0.1:PORT` (tls-terminated-tcp)

Quadlet generates multiple `ExecStartPost=` and `ExecStopPost=` lines. Requires `tailscale set --operator=$USER` for non-root access.

Port protocols are stored in the `ai.opencharly.port_proto` image label so deploy-mode commands work without access to the original layer definitions. Remote refs require `charly box pull` first — see `/charly-build:pull`.

### Instance Tunnel Inheritance

**Critical:** When deploying instances with `charly config setup -i <name>`, tunnel config is NOT auto-inherited from the base image's charly.yml entry. Each instance must have its own `tunnel:` section in charly.yml. Without it, the generated quadlet will have no `ExecStartPost=tailscale serve` commands and the instance will be unreachable via Tailscale.

**Root cause:** `sdk/deploykit/capabilities.go` deliberately skips parsing the `ai.opencharly.tunnel` OCI label — tunnel is charly.yml-only. When `charly config setup` creates a new instance, it writes ports/env/security to charly.yml but does not copy tunnel from the base entry.

**Workaround:** After `charly config setup -i <name>`, manually edit `~/.config/charly/charly.yml` to add `tunnel: {provider: tailscale, private: all}` to the instance entry, then re-run `charly config setup -i <name>` to regenerate the quadlet.

### Resolution

`tunnel` inherits from defaults (image -> defaults -> nil). The shorthand `tunnel: tailscale` defaults to `private: all` (all ports on tailnet). The shorthand `tunnel: cloudflare` defaults to `public: all`.

Source: `sdk/deploykit/tunnel_resolve.go` (resolution + the shared `schemeTarget`/`tailscaleFlag`/`isTCPFamily` helpers — the former `charly/tunnel.go` is DELETED, K-wave 2), `candy/plugin-tunnel` (the `verb:tunnel` EXECUTION leg — `tunnel_exec.go` runs the tailscale serve/funnel + cloudflared lifecycle; the former core dispatch adapter `charly/tunnel_plugin.go` is DELETED, K-wave 2), `candy/plugin-deploy-pod` / `candy/plugin-pod` (the pod-lifecycle plugins that resolve a `TunnelConfig` and drive `verb:tunnel` directly), `sdk/deploykit/quadlet.go` (systemd integration). Tunnel config is a deploy-time concern (charly.yml only) — not validated at image-validate time.

## charly.yml — Source of Truth

`~/.config/charly/charly.yml` is the **source of truth** for per-machine deployment configuration (not checked into git). All deployment commands read from image labels + charly.yml — no `charly.yml` needed.

### How it gets populated

1. **`charly config`** automatically persists: workspace, ports, env (CLI -e), env_file, network, security (auto-detected devices), volume backing (--bind/--encrypt)
2. **`charly deploy import`** merges pre-provisioned config (tunnel, volumes, DNS) from files
3. **`charly remove`** cleans the entry (use `--keep-deploy` to preserve for re-config)

### Legacy-schema rejection

The per-host config is compact node-form: top-level name-first `<name>: {<substrate-kind>: <full body>}` entries (the substrate-kind being `pod`/`vm`/`kubernetes`/`local`/`android`/`group`, its value carrying the deploy config INLINE, projected internally into the `DeployConfig.Deploy` map; the only named children are member sub-entities), with an optional top-level `provides:` directive. `LoadDeployConfig` reads it through the unified node-form loader (`LoadUnified` → `ProjectDeployConfig`). A host still on a legacy root shape — the obsolete `image:`/`images:` root key, or the legacy `deploy.yml` filename — is detected and fails loud, so a misparse can never silently drop a `volume:` entry with `type: encrypted` — the encrypted-volume load is `LoadEncryptedVolume` (sdk/deploykit/enc_probe.go) — and quietly void the encryption guarantee; the failure points at `charly migrate`.

`charly status` surfaces this as a non-fatal warning (graceful degradation falls back to image-label-driven display); the strictly-charly.yml-driven verbs (`charly deploy show`, `charly config status`, `charly start`) hard-fail. Run `charly migrate` to convert in place — it writes a rollback copy under the gitignored `<project>/.charly/backups/` tree and rewrites to the current schema. See `/charly-build:migrate` "charly migrate".

### Structure

```yaml
my-app:
  pod:
    image: my-app
    dns: "app.example.com"
    env_file: "/home/user/project/.env"
    network: charly
    engine: podman
    tunnel:
      provider: cloudflare
      port: 2283
    volume:
      - name: data
        type: bind
        host: "~/data/myapp"
      - name: workspace
        type: bind
        host: /home/user/project
        path: /workspace
      - name: secrets
        type: encrypted
    port:
      - "2283:2283"
    env:
      LOG_LEVEL: debug
    security:
      devices:
        - /dev/dri/renderD128
```

Per-deploy fields: the substrate node (`pod`/`vm`/`kubernetes`/`local`/`android`/`group`) holds the COMPLETE deploy body inline — the scalars (`image:` for a pod, `from:` to inherit a same-kind template, `disposable`, `lifecycle`, `description`, `dns`, `acme_email`, `env_file`, `network`, `engine`) AND every collection (`tunnel`, `volume`, `port`, `env` — a map, `security`, `secret`, `sidecar`, `add_candy`, `install_opts`). The only named children a deploy carries are member sub-entities (see "Sibling members" below).

### The substrate node, `add_candy:`, and `install_opts:` fields

The `charly deploy add`/`del` surface honors these per deploy, each only when relevant to the resolved target.

**The substrate node** — `pod:` (the default, carrying `image:`), `vm:`, `kubernetes:`, `local:`, or `android:` (carrying `from:`) selects the target; there is no `target:` field. A local deploy carries the Ansible-style `host:` scalar (`host: local`, the default, runs directly; `host: <user@machine>` runs over SSH) and applies its `add_candy:` candies to the host filesystem.

**`add_candy:`** — an inline list of extra layer refs applied on top of the image's base layers. Each entry accepts the same 4 ref forms as the command-line `--add-candy` flag (local name / local YAML path / remote `github.com/.../candy/<n>[@ref]`). See "add_candy: overlay mechanism" above for pod vs local semantics.

**`install_opts:`** — an inline block of local-target defaults that mirror the CLI flags on `charly deploy add`. CLI flags win on conflict; the deploy provides defaults so you don't have to repeat `--with-services --allow-repo-changes` on every invocation.

```yaml
my-host:
  local:
    from: fedora-coder                                    # inherit the kind: local template
    host: local                                           # present ⇒ local target
    add_candy:
      - my-team-vimrc                                     # local layer
      - github.com/team-acme/configs/candy/sshkeys        # remote layer
      - ./private-overlay.yml                             # local file
    install_opts:
      with_service: true
      allow_repo_changes: true
      allow_root_tasks: false
      skip_incompatible: false
      verify: true
      builder_image: fedora-builder:2026.04
    env:
      OPENCHARLY_DEV: "true"
```

Fields ignored on pod deploys: `install_opts` (local-only). Fields ignored on local deploys: `volume`, `port`, `tunnel`, `sidecar`, `security`'s container-runtime bits.

### Resource Caps

Cgroup memory and CPU limits are stored in the `security:` block of charly.yml and persist across `charly config` re-runs (a `--memory-max` flag applied once stays in effect until explicitly changed). The fields are:

```yaml
selkies-desktop:
  pod:
    image: selkies-desktop
    security:
      shm_size: "1g"
      memory_max: "6g"
      memory_high: "5g"
      memory_swap_max: "2g"
      cpus: "4.0"
```

**Merge semantics** (authoritative, from `sdk/deploykit/security.go`):

| Source | Merge rule |
|---|---|
| Layer → layer | Smallest value wins (tightest cap is the safer default) |
| Layers → image-level `security:` in charly.yml | Box-level **replaces** the merged layer value |
| Box-level → deploy-level `security:` in charly.yml | Deploy-level **replaces** the image-level value |
| CLI flag → deploy-level | CLI flag **writes** directly to charly.yml (`--memory-max=...` on `charly config`) |

Quadlet emission (`[Service]` section of `.container` file):

- `memory_max` → `MemoryMax=6G` (lowercase `g` is auto-normalized to `G` because systemd parses lowercase as `infinity` — see `/charly-selkies:chrome` gotcha)
- `memory_high` → `MemoryHigh=5G`
- `memory_swap_max` → `MemorySwapMax=2G`
- `cpus` → `CPUQuota=400%` (systemd percentage form: 1 core = 100%)

Direct-mode emission (podman run flags, for `engine.run=direct`): `--memory`, `--memory-reservation`, `--memory-swap`, `--cpus`. `SecurityArgs` in `sdk/deploykit/security.go` emits both forms from the same source of truth.

**Unset fields pass through** — setting `--memory-max=6g` alone will not wipe an existing `shm_size` from charly.yml. Only the fields you pass on the CLI get overwritten; everything else is preserved from the current charly.yml state.

**Canonical consumer:** the chrome candy's cgroup caps. See `/charly-selkies:chrome` (Resource Caps) — the caps bound a Chrome crash loop's blast radius; a wedged loop (orphan memfd shmem) is cleared by restarting the container, which tears down the cgroup. See `/charly-infrastructure:supervisord` (Event Listeners) for the eventlistener pattern in general and `/charly-image:layer` (Security Declaration) for the authoring side.

### Provides (Top-Level)

The `provides:` section holds all resolved env and MCP provides entries from deployed images. Managed automatically by `charly config` when images with `env_provide` or `mcp_provide` layers are deployed.

```yaml
provides:
  env:
    - name: OLLAMA_HOST
      value: http://charly-ollama:11434
      source: ollama
    - name: PGHOST
      value: charly-postgresql
      source: postgresql
  mcp:
    - name: jupyter
      url: http://charly-jupyter:8888/mcp
      transport: http
      source: jupyter
my-app:
  pod:
    image: my-app
```

- `provides.env:` — resolved env_provide entries with `{name, value, source}` (self-excluded per consumer)
- `provides.mcp:` — resolved mcp_provide entries with `{name, url, transport, source}` (pod-aware, no self-exclusion)
- `source` tracks which image injected each entry — used for cleanup on `charly remove`
- Priority for env (last wins): provides.env < per-image deploy env < deploy env_file < workspace .env < CLI --env-file < CLI -e
- `charly config remove` / `charly remove` automatically cleans up entries from the removed image
- Instance-aware cleanup: removing an instance (e.g., `charly remove selkies-desktop -i work`) only cleans provides entries sourced from that specific instance (`selkies-desktop/work`), not from other instances of the same base image. Base image removal requires no other instances to exist before cleaning provides

See `/charly-image:layer` for `env_provide`/`mcp_provide` field declarations and `/charly-core:charly-config` for `--update-all` propagation.

### Secrets

Per-deployment secret source overrides. Secrets declared in image labels (from `charly.yml`) are provisioned as Podman secrets at `charly config` time. A deploy's inline `secret:` list can override where the value comes from:

```yaml
my-app:
  pod:
    image: my-app
    secret:
      - name: api-key             # matches layer secret name
        source: keyring           # "keyring" (default), "env:VAR", "file:/path"
```

If no source is specified, the credential resolution chain is used: env var > keyring > config file.

### Workspace recall

Volume binding is configured at deploy time via `--bind` flags. The binding is persisted in charly.yml:

```bash
charly config my-app --bind workspace=~/project    # Saves volume config to charly.yml
charly remove my-app --keep-deploy                 # Quadlet removed, config preserved
charly config my-app                               # Picks up volumes from charly.yml
```

### Deploy status audit

```bash
charly deploy status
# sway-browser-vnc              charly.yml: yes  quadlet: yes  (ok)
# old-service                   charly.yml: yes  quadlet: no   (stale config)
# manual-service                charly.yml: no   quadlet: yes  (no overrides)
```

### Labels-only architecture

Deployment commands (`charly config`, `start`, `status`, `logs`, `update`, `remove`, `seed`, `service`) resolve all configuration from **OCI image labels** + **charly.yml** — no `charly.yml` dependency. This means you can deploy on any machine with just `charly box pull` + `charly config`.

**Local-storage requirement.** Because deploy-mode commands read OCI labels directly from local container storage (via `ExtractMetadata` → `podman inspect`), the image must be pulled first. If it isn't, the command fails with `ErrImageNotLocal` and the CLI suggests `charly box pull`. See `/charly-build:pull` for the sentinel pattern and remote-ref (`@github.com/...`) handling.

**`MergeDeployOntoMetadata` ordering gotcha.** When extending deploy-mode code, remember that deploy-overlay fields like `meta.Tunnel` are nil until `MergeDeployOntoMetadata` runs. A `if meta.Tunnel != nil` check before the merge is unreachable code — this was the actual bug fixed in `start.go` by the refactor.

### Instance Support

Deploy multiple containers of the same image with `-i <instance>`:

```bash
charly config selkies-desktop -i work -e TS_HOSTNAME=work -p 3001:3000
charly config selkies-desktop -i personal -p 3002:3000
charly start selkies-desktop -i work
charly start selkies-desktop -i personal
```

**Deploy key convention:** Base images use `selkies-desktop` as the charly.yml key. Instances use `selkies-desktop/work` (slash-separated). Functions: `DeployKey()` constructs keys, `ParseDeployKey()` splits them back (`spec/spec/deploy_key.go` — the former `charly/deploy.go` is DELETED, K-wave 2).

**Container naming:** `charly-<image>-<instance>` (e.g., `charly-selkies-desktop-work`). Quadlet file: `charly-selkies-desktop-work.container`.

**Deploy structure with instances:**

```yaml
selkies-desktop:
  pod:
    image: selkies-desktop
    port: ["3000:3000"]
selkies-desktop/work:
  pod:
    image: selkies-desktop
    port: ["3001:3000"]
    env:
      TS_HOSTNAME: work
selkies-desktop/personal:
  pod:
    image: selkies-desktop
    port: ["3002:3000"]
```

**Instance lifecycle:** All commands accept `-i`: `charly start/stop/status/logs/remove <image> -i <instance>`, `charly deploy show/reset <image> -i <instance>`. Removing an instance only cleans its charly.yml entry — the base and other instances are unaffected. Provides cleanup waits until the last entry for a base image is removed.

**Instance removal gotcha:** `charly config remove` disables the quadlet service but does NOT remove the charly.yml entry and does NOT delete the quadlet file. `charly remove <image> -i <instance>` is the complete teardown (stops the container, deletes the quadlet file, reloads systemd); `charly deploy reset <image> -i <instance>` removes the charly.yml entry. If you run `charly config --update-all` before cleaning charly.yml, stale quadlet files will be re-created. See `/charly-core:charly-config` for the removal semantics.

**MCP name disambiguation:** When an instance provides MCP servers, the server name gets `-<instance>` appended (e.g., `chrome-devtools-work`). See `/charly-core:charly-config` for details.

## Volume Backing

Layers declare what persistent storage they need via `volume:` in `charly.yml`. By default, all volumes are Docker/Podman named volumes. At `charly config` time, any volume's backing can be changed to a host bind mount or encrypted gocryptfs mount.

### Per-Volume Configuration via `charly config`

```bash
# Default: all volumes as named volumes (no flags needed)
charly config immich

# Configure specific volumes as bind mounts
charly config immich --bind import --bind external

# Bind mount with explicit host path
charly config immich --bind library=/mnt/nas/photos

# Configure volume as encrypted (gocryptfs)
charly config immich --encrypt library

# Canonical syntax: --volume name:type[:path]
charly config immich -v library:bind:/mnt/nas -v import:bind -v cache:encrypted

# Fully automated via env vars (no prompts)
CHARLY_VOLUMES_IMMICH="library:bind:/mnt/nas,import:bind" charly config immich --password auto
```

### charly.yml Volume Config

Volume backing choices are persisted in charly.yml as the deploy's inline `volume:` list:

```yaml
immich:
  pod:
    image: immich
    volume:
      - name: library
        type: bind
        host: "/mnt/nas/photos"   # explicit host path
      - name: import
        type: bind                 # no host → auto path: <volumes_path>/<image>/import
      - name: cache
        type: encrypted            # gocryptfs managed
```

**Fields:**
- `name`: matches a layer-declared volume name
- `type`: `volume` (default, named volume), `bind` (host directory), `encrypted` (gocryptfs)
- `host`: explicit host path — for `bind` type (optional, omit for auto path); for `encrypted` type, the direct volume directory containing `cipher/` and `plain/` (optional, omit to use global `encrypted_storage_path` with `charly-<image>-<name>` prefix)
- `path`: container path (only for deploy-only volumes not declared in any layer)
- `data_seeded`: `bool` — tracks whether data from image data candies was provisioned (set by `charly config`)
- `data_source`: `string` — image:tag that provided the data (updated by `charly config` and `charly update`)

**Auto path:** When `type: bind` and no `host` is specified, the host path is computed at runtime: `<volumes_path>/<image>/<name>`. Default volumes_path: `~/.local/share/charly/volumes/`. Configurable: `charly settings set volumes_path /mnt/nas/charly` (env: `CHARLY_VOLUMES_PATH`).

**Unconfigured volumes** remain named volumes — no charly.yml entry needed.

### Resolution Flow

`ResolveVolumeBacking()` in `sdk/deploykit/deploy_volume.go` splits image volumes into named volumes and bind-backed mounts:

1. Load all volumes from image labels (`ai.opencharly.volume`)
2. Load charly.yml volume overrides for this image
3. For each declared volume:
   - If charly.yml says `type=bind` → host bind mount (explicit path or auto path)
   - If charly.yml says `type=encrypted` → gocryptfs FUSE mount
   - Otherwise → named volume (Docker/Podman-managed)
4. Deploy-only volumes (with `path:` set, not in any layer) are also supported

### Integration

- **Data provisioning**: `charly config` automatically provisions data from data candies into bind-backed volumes (via `--seed`, default true). `charly update` merges new data non-destructively. See `/charly-core:charly-config` and `/charly-core:charly-update`
- **`charly shell`/`charly start`**: resolves volume backing, verifies bind dirs exist and encrypted volumes are mounted, generates `-v` flags
- **`charly config` (quadlet)**: bind-backed volumes become `Volume=` lines with host paths. `--userns=keep-id` added when bind-backed volumes exist
- **`charly remove --purge`**: removes named volumes (also encrypted volumes + the `<name>-overlay` images — see `/charly-core:remove`)
- **`charly box inspect --format bind_mounts`**: outputs deploy-configured volume backing

Source: `charly/deploy.go` (`DeployVolumeConfig`, `ResolveVolumeBacking`), `candy/plugin-deploy-pod/resolve.go` (`deploykit.ResolvedBindMount` — the former `charly/enc.go` is DELETED, K-wave 2).

## VNC Password for Deployments

For images with wayvnc (VNC on tcp:5900), provision a VNC password after enabling.
Store the password in the VNC credential store (retained host-side), then configure
server-side auth (VeNCrypt/TLS) by running the candy's `vnc: passwd` step:

```bash
charly settings set vnc.password.sway-browser-vnc mysecret   # store in the VNC credential store
charly config sway-browser-vnc
# After the container starts, configure server-side auth via the vnc: passwd step:
charly check live sway-browser-vnc --filter vnc
```

See `/charly-check:vnc` for full VNC authentication documentation.

## Port Relay Pattern

Some services (OpenClaw) bind only to loopback for security. The `port_relay` field in `charly.yml` creates a socat relay from the container's network interface to loopback, making the service accessible externally without weakening its security model.

```yaml
# candy/openclaw/charly.yml — port_relay is a CANDY field, inline in the candy body
openclaw:
  candy:
    port:
      - 18789
    port_relay:
      - 18789
```

Requires the `socat` layer as a dependency. The relay runs as a `relay-<port>` service in the configured init system. See `/charly-openclaw:openclaw` for an example.

**Chrome CDP exception:** Chrome DevTools no longer uses `port_relay`. Chrome 146+ rejects connections with non-localhost Host headers, so a simple socat relay is insufficient. Instead, Chrome uses a `cdp-proxy` Python supervisord service that listens on `0.0.0.0:9222`, forwards to Chrome on `127.0.0.1:9223` with Host header rewriting, and rewrites response URLs (e.g., `webSocketDebuggerUrl`) with Content-Length correction. See `/charly-selkies:chrome` and `/charly-check:cdp` for details.

## Provides Configuration

Global environment and MCP server injection for all deployed images. Stored in charly.yml under `provides:`.

### Structure

```yaml
provides:
  env:
    - name: OLLAMA_HOST
      value: http://charly-ollama:11434
      source: ollama
  mcp:
    - name: jupyter
      url: http://charly-jupyter:8888/mcp
      transport: http
      source: jupyter
```

- `provides.env:` — resolved env_provide entries with `{name, value, source}`
- `provides.mcp:` — resolved mcp_provide entries with `{name, url, transport, source}`
- `source` field tracks which image contributed each entry (used for cleanup on `charly remove`)
- Entries resolved at `charly config` time from layer `env_provide:` and `mcp_provide:` declarations
- `GlobalEnvForImage()` in `provides.go` resolves both env and MCP provides for each consumer image
- Env provides: self-excluded (prevents own env_provide from overriding service bind addresses)
- MCP provides: pod-aware (same-container entries resolve to `localhost`, no self-exclusion)
- Consumer containers receive `CHARLY_MCP_SERVERS` JSON env var with resolved MCP server entries

See `/charly-core:charly-config` for setup workflow and `/charly-image:layer` for declaration format.

## Sidecar Pod Deployment

When sidecars are attached via `charly config --sidecar <name>`, deployment generates a Podman **pod** instead of a standalone container. See `/charly-automation:sidecar` for full sidecar documentation.

### Generated Files

| File | Content |
|------|---------|
| `charly-<image>.pod` | Pod: `Network=charly`, `PodmanArgs=-p` (ports), `--shm-size` |
| `charly-<image>-<sidecar>.container` | Sidecar: image, env, caps, devices, secrets |
| `charly-<image>.container` | App: `Pod=charly-<image>.pod`, no ports/network |

The pod owns the shared network namespace. Port mappings and ShmSize move from the container to the pod. The app container gets `Pod=` and loses `PublishPort=` and `Network=`.

### Dual Networking (Tailscale)

When a Tailscale sidecar is attached, the pod has dual networking:
- **"charly" bridge** — container-to-container connectivity, `env_provide` discovery
- **Tailscale tun** — exit node routing for outbound internet traffic
- `--exit-node-allow-lan-access` exempts bridge subnets from the tunnel

Host `tunnel: tailscale` (ExecStartPost=tailscale serve) and the sidecar are **independent**: the host tunnel serves ports on the host's tailnet, while the sidecar handles exit node routing on a potentially different tailnet.

### charly.yml Sidecar Config

```yaml
selkies-desktop:
  pod:
    image: selkies-desktop
    sidecar:
      tailscale:
        env:
          TS_HOSTNAME: selkies-desktop
          TS_EXTRA_ARGS: "--exit-node=100.80.254.4 --exit-node-allow-lan-access"
```

## Preemptible resource arbitration (`preemptible` / `requires_exclusive` / `requires_shared` / `charly preempt`)

A physical host resource can be held by only ONE deployment at a time — the
canonical case is a GPU passed through to a VM via VFIO. The resource arbiter
(the compiled-in `verb:arbiter`, `candy/plugin-preempt`) frees such a resource
on demand and gives it back.

```yaml
gpu-workstation:                   # HOLDER — a long-running operator VM
  vm:
    from: gpu-workstation-vm
    preemptible:
      holds: [nvidia-gpu]          # exclusive-resource token(s) it occupies (shorthand: preemptible: [nvidia-gpu])
      stop: shutdown               # graceful shutdown (default & only) — frees a VFIO device
      restore: always              # always (default) | on-success

# CLAIMANT (any deploy, including a disposable check deploy) that needs sole use while it runs:
check-gpu-bed:
  vm:
    from: gpu-check-vm
    disposable: true
    requires_exclusive: [nvidia-gpu]
```

- **When the arbiter acts.** Before a claimant is brought up — `charly check run <bed>`
  (transient claim, auto-released at teardown), or a standalone `charly vm create` /
  `charly start` (persistent claim, released on `charly vm stop`/`vm destroy`/`charly stop`/
  `charly remove`) — it gracefully stops every running preemptible holder whose
  `holds:` intersects the claimant's `requires_exclusive:`, waits for it to
  actually power off (so the resource is truly released), records a crash-safe
  lease, then proceeds. Nested `charly` subprocesses inherit the lease
  (`CHARLY_PREEMPT_LEASE`) and never re-acquire.
- **`requires_shared:` — the REFCOUNTED claimant** (mutually exclusive with
  `requires_exclusive:` on one node, per `validate_preempt.go`). Many concurrent
  `requires_shared:` claimants share a token, so a GPU-CDI **pod** declares
  `requires_shared: [nvidia-gpu]` and the arbiter flips the whole IOMMU group
  **vfio→nvidia+CDI** at the first claimant's deploy and back **nvidia→vfio** only
  when the LAST claimant releases — the group-aware `modprobe`-gated switch
  (`charly vm gpu mode`). A `preemptible` holder whose `holds:` intersects a shared
  claim is still preempted first. Full GPU-mode-switch model: `/charly-vm:vm` +
  `/charly-internals:disposable`; the bed surface: `/charly-check:check`.
- **Standing authorization — preempt autonomously.** You may trigger preemption
  (bring up a `requires_exclusive:` claimant, stopping a running `preemptible`
  holder) WITHOUT per-run operator confirmation. Preemption is reversible by design
  (graceful stop + guaranteed `restore`), so it is exempt from the
  confirm-before-destroy rule that governs irreversible teardown of a
  non-preemptible resource. See `/charly-internals:disposable` "Standing
  authorization".
- **Token = a name, not a mechanism** — operator-chosen (`nvidia-gpu`), decoupled
  from how each side reaches it (VM hostdev vs pod `--device`); pure
  set-intersection unifies pod-vs-VM contention.
- **GPU auto-allocation** — when a token ALSO carries an embedded `resource:`
  `gpu:` selector (`resource: {nvidia-gpu: {gpu: {vendor: "0x10de"}}}`), a
  VM-targeted claimant's `charly vm create` auto-detects the matching GPU,
  persists its `<hostdev>` into the per-host `instance.yml`, and injects it — or
  FAILS HARD when no matching card exists. Operator-authored hostdevs win (no
  double-inject). See `/charly-internals:disposable` "resource-arbitration axis"
  + `/charly-build:build` `resource:`.
- **`charly preempt status`** lists active leases + flags STRANDED ones (claimant
  gone). **`charly preempt restore [claimant]`** reconciles stranded leases (also run
  automatically at the next acquire) / force-releases a named one. A holder is
  NEVER left permanently stopped — the ledger
  (`~/.local/share/charly/preemption/leases.yml`) is written before any stop, and
  restore = "start every listed holder that isn't running".
- **Orthogonal to disposable/ephemeral** — no derivation either way; a deploy may
  be both preemptible and disposable. Full reference: `/charly-internals:disposable`
  "The resource-arbitration axis".

## Deploy-into nesting — run a `local:` deploy INSIDE a VM (or pod)

A resource node placed **under another resource node** deploys **INTO** that node's venue.
Nesting is **tree position** — there is no `nested:` field, and the child carries **no `host:`
field**. The executor is derived from the tree: `kit.NestedExecutor{Parent, Jump}` composes
onto the parent's venue (`Venue()` → `nested:<jump>/ssh://arch@…`), so a `local:` child of a
`vm:` node runs its layer-application **in the guest**, driven from the host. The child is
addressed by dotted path (`<parent>.<child>`).

**This is how you run `local:`-target testing inside a VM instead of on the operator's
machine** — no `host:` retarget, no `charly --host`, no charly on the guest's PATH (the vm
deploy stages the host binary into the guest for its own use).

Canonical worked example — `check-arch-vm` in `box/arch/charly.yml` (a `vm:` bed with a
nested `local:` child whose checks assert the ledger in the GUEST's home):

```yaml
check-arch-vm:
  vm:
    from: arch
    disposable: true
  arch-host:                       # tree position ⇒ deploys INTO the VM above
    local:                         # NO host: field — the venue comes from the parent
      disposable: true
      add_candy:
        - '@github.com/opencharly/charly/candy/direnv'
      plan:
        - check: command=test -d /home/arch/.config/opencharly/installed/deploys
          id: ah-ledger-deploys-dir       # the ledger lands in the GUEST, not the host
          context: [runtime]
          command: "test -d /home/arch/.config/opencharly/installed/deploys"
        - check: command=direnv --version
          id: ah-direnv-version
          context: [runtime]
          command: "direnv --version"
```

Contrast the three ways a `local:` deploy reaches a machine — they are NOT interchangeable:

| Shape | Executor | Where the layers land |
|---|---|---|
| `local: {host: local}` (or `host:` absent), **top-level** | `ShellExecutor` | this machine, direct shell |
| `local: {host: user@machine}`, **top-level** | `SSHExecutor` | that remote machine |
| `local:` **nested under** a `vm:`/`pod:` node | `NestedExecutor` over the parent venue | inside the enclosing deployment |

A top-level `local:` bed marked `disposable: true` therefore applies its candies to the
OPERATOR'S workstation. Nest it under a disposable `vm:` bed to keep the same `local:`
authoring shape while writing only inside the guest.

**What nesting does NOT preserve.** `ShellExecutor` runs ONLY for the table's FIRST row — a
top-level `local:` deploy whose `host:` is `local` or absent. A nested child runs on
`NestedExecutor` over the parent's venue — for a `vm:` parent, the guest `SSHExecutor` — and
`RootExecutorForDeployNode` explicitly "does NOT handle the nested-inside-a-parent case"
(`sdk/deploykit/deploy_chain.go`). So **the other two rows — a top-level `host:` retarget
(`SSHExecutor`) and a nested child (`NestedExecutor`) — both bypass `ShellExecutor`**, nesting
included. Relocating a `host: local` bed by either means deletes its `ShellExecutor` +
`HostDeployTarget` coverage, which must then be replaced deliberately by another top-level
`host: local` bed — never assumed to have moved with it.

## Sibling members — companion deployments brought up alongside

A resource node placed directly **under a deploy** is a sibling **member** — a
companion deployment brought up **ALONGSIDE** it on the shared `charly` network
(*siblings*, not children). Contrast deploy-into nesting (a resource node placed
**under another resource**), whose children run **inside** that node's venue and
are addressed by a dotted path (`parent.child`). Membership is **tree position** —
there is no `peer:` or `nested:` field. A member is a companion **instrument**: the
canonical case is a Chrome DRIVER pod that CDP-probes a SEPARATE web-server
SUBJECT, where the driving probe is a check step placed under the driver member and
addresses the subject via `${HOST:<subject>}` (see `/charly-check:check`
"Cross-deployment probing"). The SAME tree position + lifecycle serve a disposable
check deploy and an operator deploy — one codebase.

```yaml
webapp:                            # an operator deploy + a companion member
  pod:
    image: web
  chrome:                          # a SIBLING member, brought up on the shared charly net
    pod:
      image: chrome-headless       # a full deploy (its own substrate/port/…)
    chrome-step-render:            # a driven probe — runs on chrome (tree position)
      check: cdp renders the web subject over the shared net
      cdp: text
      url: http://${HOST:webapp}:8080   # address the subject member by HOST var
```

- **Folded to addressable top-level entries.** At load time `foldMembers` reads each
  tree-position member into the internal `.Members` map and registers it as a top-level
  Deploy entry (so `charly config <member>` / `charly start <member>` resolve it
  through the exact path any deploy uses) carrying a derived `MemberOf: <owner>`. A
  member name must be **globally unique** (a collision with any deploy/bed/member is
  a hard load error) and carry **no `.`** (same rules as nested-member keys + bed
  names); the author keeps member **host ports disjoint** (the loader does not check
  ports — port absence, i.e. auto-allocation, avoids fixed-port collisions).
- **One shared lifecycle (R2).** `BringUpMembers` / `TearDownMembers` (`sdk/deploykit/deploy_members.go`)
  bring members up after the owner and tear them down with it, by shelling out to the
  SAME verbs — a pod member via `charly config` + `charly start` (+ readiness wait), a
  non-pod member via `charly deploy add` / `charly deploy del`. Wired into `charly deploy add` /
  `charly deploy del` (operator path) AND the disposable-deploy bed runner (the bed's
  `--node-only` add never double-deploys; the runner brings members up after the root
  starts). A bed's `charly update` (destroy + rebuild) tears members down and back up too.
- **Disposability is inherited, never invented.** `foldMembers` promotes a member to
  `disposable: true` only when its OWNER is disposable (so a disposable bed's
  rebuild is authorized to tear the member down); a member of a non-disposable operator
  deploy stays non-disposable. No new autonomy is granted — members are components of
  their owner, touched only by the owner's explicit add/del/update (R6,
  `/charly-internals:disposable`).
- **Members are NOT check-live'd.** A disposable check deploy evaluates its SUBJECT
  (root + any nested members via `bedCheckLiveRefs`); sibling members are instruments,
  never evaluated themselves — the subject's checks placed under a member drive
  *through* it.
- **Addressing the subject from a driven probe** uses the unified `${HOST:...}` variable —
  `${HOST:<member>}` (no `:<port>` → container DNS) or `${HOST:<member>:<port>}` (with
  `:<port>` → host-reachable endpoint) — see `/charly-check:check` "Cross-deployment
  probing".

## Cross-References

**Deploy surface:**
- `/charly-local:local-deploy` — Local-target execution model: the external `deploy:local` plugin, ledger, gates, ReverseOp kinds, sudo batching
- `/charly-internals:install-plan` — The InstallPlan IR shared by the deploy targets: ALL FIVE substrates (local/vm/pod/kubernetes/android) are external out-of-process deploys via plugins over the executor reverse channel — local/vm consume the IR via `kit.WalkPlans` (vm's walk runs inside the guest); pod's plugin walks nothing — its candy `plugin-deploy-pod` lifecycle builds the overlay host-side: the core `overlay` host-builder runs the prep+resolve M-seam, the candy renders the overlay in its own code via `deploykit.OCITarget` + `deploykit.NewRenderGeneratorFromProject` (`add_candy:` synthesis, P11c); `charly box build` itself emits via the separate WriteCandySteps → EmitTasks generator in `sdk/deploykit` (relocated in #67), not the IR
- `/charly-internals:local-infra` — Supporting Go files for local deploys: hostdistro, ledger, builder_run, shell_profile, reverse_ops, `sdk/deploykit/compile_service_steps.go` (service_render, relocated), `candy/plugin-fleet/deploy_ref.go` (deploy_ref, relocated)

**Deploy-adjacent commands:**
- `/charly-build:pull` — Prerequisite: fetch the image into local storage; handles remote refs (`@github.com/...`) and the `ErrImageNotLocal` recovery path
- `/charly-automation:sidecar` — Sidecar containers, pod networking, Tailscale exit nodes, Environment Contract (provides filtering)
- `/charly-core:service` — Service lifecycle (start/stop/update/remove)
- `/charly-core:start` — Ergonomic alias for `charly deploy add <image> <image>` (container target)
- `/charly-core:stop` — Ergonomic alias for `charly deploy del <name>`
- `/charly-core:charly-update` — Per-instance update pattern; equivalent to `charly deploy add <name> --pull`
- `/charly-core:charly-config` — Resource cap flags (`--memory-max/high/swap/cpus`), provides filtering, env_require enforcement, NO_PROXY auto-enrichment, `--sidecar`, `-i` instance support, MCP name disambiguation
- `/charly-automation:enc` — Encrypted storage commands (charly config mount/unmount)
- `/charly-check:vnc` — VNC password setup for desktop containers
- `/charly-vm:vm` — Virtual machine deployment (charly vm)
- `/charly-build:build` — Building images before deployment (+ the `--no-cache` intermediate scratch-stage caveat)
- `/charly-build:charly-mcp-cmd` — verify the MCP endpoints declared by `provides.mcp:` entries are actually reachable (`charly check live <image> --filter mcp`); note the **port-publishing gotcha** when a `port:` override in charly.yml predates a newly-added mcp-providing layer
- `/charly-image:image` — Image configuration, OCI label emission, `sdk/deploykit/capabilities.go` tunnel read-skip
- `/charly-image:layer` — Unified `service:` schema (use_packaged + structured custom), `env_provide`/`env_require`/`env_accept` field declarations, security resource caps
- `/charly-check:check` — Local `check:` in charly.yml overlays image-baked deploy defaults: entries with matching `id:` replace, otherwise append. `id: X, skip: true` disables a baked check without a replacement.

**Canonical layer worked examples:**
- `/charly-selkies:chrome` — cgroup resource-caps consumer (caps bound a Chrome crash loop)
- `/charly-infrastructure:supervisord` — Event listener pattern triggered by the caps; ServiceSchemaDef that renders `service:` entries to supervisord INI
- `/charly-infrastructure:postgresql` — Canonical `use_packaged:` entry (packaged unit reuse)
- `/charly-ollama:ollama`, `/charly-hermes:hermes` — Custom `service:` entries
- `/charly-selkies:selkies-labwc` — Multi-instance proxy deployment, tunnel inheritance workaround

## Cross-kind name reuse + ResolveDeployRef precedence

A deploy's name lives alongside every other top-level entity in its document. Cross-kind reuse of a name across SEPARATE discovered files is fine (a `candy/redis` + a `box/redis` resolve to distinct internal maps), but two top-level entities WITHIN one document MUST NOT share a name — they would collide on one YAML key (`charly box validate` flags it). The convention is to keep the user-facing deploy name and **suffix** the template it deploys: this repo's `charly-cachyos` deploy carries `local: {from: charly-cachyos-app}`, referencing the distinct `charly-cachyos-app` kind:local template. A deploy's substrate node (`pod:`, `vm:`, `local:`, `kubernetes:`, `android:`) is scoped to the matching kind, no fall-through.

`ResolveDeployRef` (used by `charly deploy add <name> <ref>`) is box-first: when a name exists as BOTH a box and a candy, the box wins for the primary `<ref>` positional. The `--add-candy <ref>` path goes through `ResolveDeployRefAsCandy`, which is candy-first. A same-name box and candy (in separate files) is permitted.

The loader raises a hard load-time error on obsolete deploy names (the retired `qc` / `cachyos-dx` keys) and on any obsolete root shape (a `kind: deployment` / `deployment:` / `kind: deploy` / `deploy:` / `fleet:` document — the kind is now the name-first substrate-node (`pod:`/`vm:`/`kubernetes:`/`local:`/`android:`/`group:`) discriminator); every such error points at `charly migrate`. See `/charly-build:migrate`.

## When to Use This Skill

**MUST be invoked** when the task involves quadlet generation, tunnels, bind mounts, or deploy overlays. Invoke this skill BEFORE reading source code or launching Explore agents.

**Workflow position:** After `/charly-build:build`, before `/charly-core:service`.
Previous step: `/charly-build:build` (build the image). Next step: `/charly-core:service` (start, status, logs).

Live-deploy verification: see /charly-check:check (the 11 Testing Standards) and /charly-internals:disposable.
