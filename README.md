# pvectl

Go CLI and library for the LabUL Proxmox template work: find VMs and
templates, check their configs, follow tasks, migrate, and promote a fresh
Packer template (FS9100 copy for Terraform, move to ul-pve11, print the
repins). It replaces `hack/labul/pve.py` and the one-off scripts of the
2026-11 template rounds ([stuttgart-things#4652](https://github.com/stuttgart-things/stuttgart-things/issues/4652)).

> **VM descriptions (notes) are never printed.** In the LabUL-Template pool
> they hold plaintext passwords. The library drops `description` when it reads
> a config, and no command or flag shows it. `cipassword` shows as `(set)`.

## Install

```bash
# release binary (linux/darwin/windows, amd64/arm64)
gh release download -R stuttgart-things/pvectl -p 'pvectl_*_linux_amd64.tar.gz' -O - | tar xz pvectl
install -m 0755 pvectl ~/.local/bin/

# or from source
go install github.com/stuttgart-things/pvectl@latest

# container image (for Dagger/Tekton): ghcr.io/stuttgart-things/pvectl
```

## Credentials

From the environment only, never printed, never stored:

| Variable | |
|---|---|
| `PVE_API_URL` | `https://<host>:8006/api2/json` |
| `PVE_API_USER` | `user@realm` (ticket login, user/password) |
| `PVE_API_PASSWORD` | |
| `PVE_CA_CERT` | optional: PEM of the cluster CA (`/etc/pve/pve-root-ca.pem` on any node) |

```bash
sops exec-env ~/.secrets.d/labul/proxmox.env 'pvectl vms ubuntu26'
```

TLS is verified against `PVE_CA_CERT`, else the system store. The lab's
certificate is in neither by default; `--insecure` skips the check for one run.

## Two modes, same logic

The commands are thin surfaces over the library in `pkg/`. A run is
**interactive** when stdin and stdout are a TTY, there is no `-o json`, and
there is no `--non-interactive`. Otherwise it is **programmatic**.

| | interactive (human at a terminal) | programmatic (script, CI, Dagger/Tekton, AI agent) |
|---|---|---|
| missing input | asked for: VM/template from a filterable list, target node, storage | error naming the flag (exit 2, `"flag": "--vmid"` in JSON) |
| writes | summary + confirmation (`--yes` skips it) | need `--yes`, or `--dry-run` for the plan |
| long tasks | spinner, progress bar from the task log (`(x%)`), last log line | log lines on stderr |
| output | readable text | `-o json`: one JSON document on stdout, logs/progress on stderr |

Every prompt has a flag counterpart. `pvectl <cmd> --help` shows examples
for both modes.

## Commands

| Command | | Writes? |
|---|---|---|
| `vms [filter]` | VMs (`--templates`, `--all`) by name substring or VMID; with a filter also guest IPv4 and the `qm*`/`ha*` tasks of the last 4 days (`--days`) | no |
| `config [vmid]` | config without description, disks, checks: `cpu: host`, guest agent, cloud-init drive | no |
| `tasks` | recent tasks (cluster, or `--node`/`--vmid`/`--type`/`--errors`/`--since`) | no |
| `tasks --follow <upid>` | follow a task until it stops; exit 5 if it failed | no |
| `ha [<node>] [vmid]` | HA resource and state, recent tasks, log of the last start/migrate | no |
| `repin` | the stuttgart-things values for a new template/copy VMID | no |
| `migrate [<node> <vmid> <target>]` | online if running, with local disks if any, waits for the task; `--targetstorage` | yes |
| `template promote [vmid]` | checks → migrate `--to` → FS9100 copy `--fs9100-copy` → repins | yes |
| `start [<node>] [vmid]` | start a stopped VM, wait for its IP; refuses on HA `error` | yes |
| `ha-recover [<node>] [vmid]` | HA `error` → `disabled` → `started`, wait for the IP (#4240) | yes |

Every write command has `--dry-run` (the plan: API calls, new VMID/name;
nothing is written) and `--yes`. A description parameter shows as
`(not shown)` in a plan.

### config checks

| Check | Why |
|---|---|
| `cpu` = `host` | EL10 needs x86-64-v3; the Packer plugin did not always carry `cpu_type` through (#4650) |
| `agent` enabled | without it Proxmox never learns the guest IP |
| cloud-init drive | a VM without one keeps the template hostname. On a **template** a missing drive is only a warning (`!`): none of the LabUL templates (211, 266, 280, 282) has one, because bpg (`initialization`) and Telmate add it to every clone |

A failed check exits with 4. Warnings do not.

### template promote

```text
Packer (ul-pve01) ──> 1. checks ──> 2. migrate --to ul-pve11 ──> 3. --fs9100-copy ──> 4. repins (printed)
```

1. **checks**: as in `config`. A failure stops the run (exit 4) unless you pass `--skip-checks`.
2. **migrate**: the template moves to `--to` (ul-pve11), so the proxmoxvm chart can leave `templateNode` empty.
3. **FS9100 copy**: a full clone `<name>-fs9100` with `storage=FS9100-01-1`, `target=ul-pve01` and
   `pool=stuttgart-things`, which is then converted to a template. Telmate Terraform (create-terraform-vm)
   sends no `storage` on clone and gets 403 (`Datastore.AllocateSpace`) on DD-sthings, so it clones the
   copy. These are the same calls as `pve-fs9100-copy.py`, which made 282 on 2026-11-16.
4. **repins**: printed, not applied. The PRs are made by hand:
   - `crossplane/platform/capabilities/proxmoxvm/values.yaml`: `templateVmId` (ubuntu26: `environments.labul.templateVmId` and `templates.ubuntu26.templateVmId`; other OSes only `templates.<os>.templateVmId`). Then bump the chart, make a flux release and tag flux-infra.
   - `backstage/templates/create-terraform-vm/{template.yaml,machinery-cluster-labul.yaml}`: the `vm_clone_id` default, in both files together.

   `--repo <stuttgart-things checkout>` adds the current values.

The command is **idempotent**. A template that is already on `--to` is not
migrated again, and a copy that already exists by name is reported instead
of cloned twice. After a failure, run the same command again. Old templates
that are still pinned (211, 266) are never touched: promote only migrates
its source and creates the copy.

```bash
E=~/.secrets.d/labul/proxmox.env
sops exec-env $E 'pvectl --insecure template promote 290 --to ul-pve11 --fs9100-copy --dry-run'
sops exec-env $E 'pvectl --insecure template promote 290 --to ul-pve11 --fs9100-copy --repo ~/projects/stuttgart-things'
# CI / agent
sops exec-env $E 'pvectl --insecure template promote 290 --to ul-pve11 --fs9100-copy --yes -o json' | jq '.plan.repins'
```

## JSON contract (`-o json`)

- stdout carries exactly one JSON document: an array for `vms`/`tasks`, an
  object otherwise. Logs, task log lines and progress go to stderr.
- Errors are JSON too: `{"error": "...", "code": 2, "kind": "usage", "flag": "--vmid"}`.
  `flag` is set when a missing or invalid flag caused the error.
- Commands that produced a result and still fail print the result, not an
  error object, and signal the failure through the exit code. Examples: `config` with a failed check (exit 4),
  `vms` without a match (`[]`, exit 3), `promote`/`migrate` after a failed
  task (`"state": "failed"`, `error` set, exit 5).

## Exit codes

| Code | Kind | Meaning |
|---|---|---|
| 0 | | ok, including "nothing to do" |
| 1 | `error` | API, network or TLS error |
| 2 | `usage` | bad or missing flag/argument, missing environment, write without `--yes` in programmatic mode |
| 3 | `not_found` | VM, template, node or task not found; `vms` without a match |
| 4 | `check_failed` | config check failed, or the promote source is not a template |
| 5 | `task_failed` | a Proxmox task ended with an error |
| 130 | `aborted` | aborted at a prompt or by Ctrl+C (Ctrl+C while following a task detaches; the task keeps running) |

## Library

```go
import (
    "github.com/stuttgart-things/pvectl/pkg/pve"      // client, configs (no description), checks, tasks, migrate, clone, HA
    "github.com/stuttgart-things/pvectl/pkg/promote"  // Build (plan, read-only) and Execute
    "github.com/stuttgart-things/pvectl/pkg/repin"    // the stuttgart-things changes after a promotion
)

o, _ := pve.OptionsFromEnv()
c, _ := pve.New(o)
plan, err := promote.Build(ctx, c, promote.Options{SourceID: 290, To: "ul-pve11", Copy: true})
```

`pkg/pve/pvetest` is a fake Proxmox API for tests.

## From pve.py

| pve.py | pvectl |
|---|---|
| `pve.py vms <filter>` | `pvectl vms <filter>` |
| `pve.py ha <node> <vmid>` | `pvectl ha <node> <vmid>` |
| `pve.py start <node> <vmid>` | `pvectl start <node> <vmid>` |
| `pve.py ha-recover <node> <vmid>` | `pvectl ha-recover <node> <vmid>` |
| `pve.py migrate <node> <vmid> <target> [--targetstorage S]` | `pvectl migrate <node> <vmid> <target> [--targetstorage S]` |
| `pve.py --insecure …` | `pvectl --insecure …` |
| `pvecfg`, `pve199`, `pvefind`, `pvetpl`, `pvetask`, `pve-fs9100-copy` | `config`, `vms --templates`, `tasks --follow`, `template promote --fs9100-copy` |

Writes now need a confirmation or `--yes`. pve.py wrote without asking.

## Development

```bash
task test          # go test ./... in Dagger (the dev sandbox breaks the Go linker for go test)
task build-local   # ./bin/pvectl with version info
task smoke         # read-only checks against LabUL through sops
```

Releases come from the Release workflow (semantic-release + GoReleaser + ko)
once "Go Lint, Test & Build" and the image build are green on `main`. PR
titles must be Conventional Commits.
