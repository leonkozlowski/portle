<div align="center">

# portle

### Save and manage repeatable Kubernetes port-forwards by name.

[![CI](https://github.com/leonkozlowski/portle/actions/workflows/ci.yml/badge.svg)](https://github.com/leonkozlowski/portle/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-7c3aed.svg)](LICENSE)

</div>

Portle keeps repeatable `kubectl port-forward` commands in one small config file. Forwards run in the background, survive after the CLI exits, and can be managed by name or selected interactively.

```console
$ portle up web
↑ web started → http://127.0.0.1:19400

$ portle list
> Port forwards [1 active · 1 configured]

  Name    Resource    Local    Remote    Status    Protocol    Namespace    Context
  web     svc/web     19400    80        ↑ Up      http        default      current

$ portle down web
↓ web stopped
```

## Install

Install the latest macOS or Linux release to `~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/leonkozlowski/portle/main/install.sh | sh
```

Or install with Go:

```bash
go install github.com/leonkozlowski/portle/cmd/portle@latest
```

Portle requires `kubectl` and access to a Kubernetes cluster. [Portless](https://github.com/vercel-labs/portless) is optional.

## Quick start

Create `~/.config/portle/config.yaml`:

```bash
portle init
```

`portle init` creates an empty config:

```yaml
targets: []
```

Run `portle add` to create a target with a guided wizard. It asks for the resource, ports, protocol, and other settings one at a time, then shows the finished target for review before saving.

For example, a target created with the current kubectl context, the `default` namespace, and an automatically selected local port looks like this in the config:

```yaml
targets:
  - name: web
    resource: svc/web
    remote_port: 80
    protocol: http
```

You can also add a running pod directly without the wizard. If the pod declares exactly one container port, Portle infers it:

```bash
portle add web-7d9f6c8b5f-x2k4m --namespace default --name web --protocol http
```

For pods with no declared port or multiple ports, select one with `--port 8080`.

Then bring it up:

```bash
portle up web
portle open web
portle down web
```

Leave off the name to choose interactively with the arrow keys and Enter. `portle up` lists configured targets that are down; `portle down` lists active forwards. In scripts and redirected commands, pass the name explicitly.

### Edit and delete targets

Use the same list picker to edit or delete a target:

```bash
portle edit
portle delete
```

`portle edit web` skips the target picker and opens the guided editor with the target's current values filled in. Portle shows a final review and validates the result before atomically updating the config. Bring an active target down before editing it.

Deletion uses a small Yes/No picker and automatically brings down an active forward before removing it. Pass the target and `--yes` for scripts:

```bash
portle delete web --yes
```

Running `portle` without a command is the same as `portle list`. `portle ls` is available as a short alias.

## Commands

| Command | Description |
| --- | --- |
| `portle add [pod]` | Add a target with the wizard, or add a pod directly |
| `portle edit [name]` | Edit a target with the wizard, selecting one when omitted |
| `portle delete [name]` (`remove`, `rm`) | Delete a target, selecting one when omitted |
| `portle up [name]` | Bring up a forward, selecting one when omitted |
| `portle down [name]` (`stop`) | Bring down a forward, selecting one when omitted |
| `portle list` (`ls`) | List targets and their status |
| `portle open <name>` | Open a running HTTP target |
| `portle doctor` | Check kubectl, cluster access, config, and state |
| `portle init` | Create an empty config |

Run `portle --help` or `portle <command> --help` for contextual help.

## Configuration

| Field | Required | Default | Description |
| --- | :---: | --- | --- |
| `name` | yes | — | Unique target name |
| `resource` | yes | — | Kubernetes resource, such as `svc/web` |
| `remote_port` | yes | — | Port exposed by the resource |
| `namespace` | no | `default` | Kubernetes namespace |
| `local_port` | no | automatic | Exact local port to use |
| `protocol` | no | `tcp` | `tcp` or `http` |
| `context` | no | current | kubectl context |
| `portless` | no | `false` | Give an HTTP target a `.localhost` URL |

Automatic ports come from `19400-19499`. If an explicit `local_port` is already occupied, Portle reports the collision instead of choosing another one.

### Portless

For a stable URL such as `https://web.localhost`, install [Portless](https://github.com/vercel-labs/portless) and enable it on an HTTP target:

```yaml
targets:
  - name: web
    resource: svc/web
    remote_port: 80
    protocol: http
    portless: true
```

Portle registers and removes the matching Portless alias with the forward.

## Files

Portle keeps its config, state, and lock together:

```text
~/.config/portle/
├── config.yaml
├── state.json
└── state.lock
```

State writes are locked and atomic. Portle verifies a process fingerprint before reusing or stopping a saved PID, preventing stale PIDs from being treated as owned processes.

## Development

```bash
go vet ./...
test -z "$(gofmt -l .)"
go build ./cmd/portle
```

## License

[MIT](LICENSE)
