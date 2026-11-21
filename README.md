# Terraform Provider: opsy

`opsy` turns any API, CLI or script into a Terraform resource without writing a new provider.

You describe each operation (create, read, update, delete...) as a **stack** in a YAML file. The provider runs those stacks, using the [seristack](https://github.com/TechXploreLabs/seristack) package for execution, and maps their JSON output to Terraform state.

- Documentation for seristack: <https://seristack.getsaas.in>
- Stacks can run **locally** (in the provider process) or on a **seristack HTTP server** behind your own authentication.
- One generic resource, data source, ephemeral resource and action cover anything you can drive from a script or CLI, for example the OCI, AWS or Azure CLIs, REST calls and internal tools.

## Contents

- [How it works](#how-it-works)
- [Requirements](#requirements)
- [Installation](#installation)
- [Provider configuration](#provider-configuration)
- [Resource: `opsy_seristack`](#resource-opsy_seristack)
- [Data source: `opsy_seristack`](#data-source-opsy_seristack)
- [Ephemeral resource: `opsy_seristack`](#ephemeral-resource-opsy_seristack)
- [Action: `opsy_seristack`](#action-opsy_seristack)
- [The stack contract](#the-stack-contract)
- [Long-running operations](#long-running-operations)
- [Security](#security)
- [Development](#development)
- [License](#license)

## How it works

```
 Terraform ── opsy provider ──┬── local mode ── seristack package ── your CLI / script / API call
                              │
                              └── http mode ─── seristack server (nginx, oauth2-proxy ...) ── stack
```

1. You write stacks in YAML (a stack is a few shell commands that print JSON).
2. The `opsy_seristack` resource calls the stack that matches each lifecycle step: `CREATE`, `READ`, `UPDATE`, `DELETE`.
3. The stack prints a JSON document with the resource `id` and an `output` object. The provider stores them in state.

## Requirements

| Item | Version |
|---|---|
| Terraform | 1.10+ for the ephemeral resource, 1.11+ for the write-only `sensitive` attribute, 1.14+ for actions (OpenTofu is not tested) |
| Go (to build) | see `go.mod` |
| seristack | v0.4.11 (pinned in `go.mod`); for `http` mode, a seristack server of the same version. v0.4.10 and later redact values marked sensitive in their logs and errors. |

## Installation

```hcl
terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}
```

## Provider configuration

Set **at most one** of the `local` and `http` blocks. With neither, only the provider functions are usable.

### Local mode

Stacks run inside the provider process. Each resource `type` maps to a file `<type>.yaml`.

```hcl
provider "opsy" {
  local {
    folder_path = "${path.module}/stacks"   # a directory of .yaml / .yml files
    # path      = "${path.module}/stacks.zip"  # or one zip file (set exactly one of the two)
  }
}
```

In local mode the stacks inside `<type>.yaml` are named after the lifecycle step: `CREATE`, `READ`, `UPDATE`, `DELETE`, and so on.

### HTTP mode

Stacks run on a seristack server. Each call goes to `<url>/<type>_<STACK>`.

```hcl
provider "opsy" {
  http {
    url = "https://seristack.example.com/oci/"

    # Authentication runs on THIS machine: a local "provider stack" prints JSON,
    # and the keys listed in `output` are sent on every request.
    provider_stack_file = "${path.module}/auth.yaml"
    provider_stack_name = "auth0"
    vars = {
      token_url     = "https://example.auth0.com/oauth/token"
      client_id     = var.client_id
      client_secret = var.client_secret
      audience      = "https://api.example.com"
    }
    output = ["access_token"]
  }
}
```

| Attribute | Description |
|---|---|
| `url` | Base URL of the server. Required when the `http` block is present. Use `https://`: the provider warns when credentials would travel over plain HTTP to a non-local host. |
| `provider_stack_file` | Local YAML file containing the authentication stack. |
| `provider_stack_name` | Name of the stack in that file. It runs on your machine the first time credentials are needed, again when they expire, and again when the server answers 401. |
| `vars` | Sensitive map of variables passed to the provider stack. Two keys are also read by the provider itself: `ca_cert_file` (PEM bundle to trust) and `timeout` (per-request Go duration, default `2m`). |
| `output` | Keys of the provider stack's JSON output to send on every request. `access_token` is sent as `Authorization: Bearer <value>`; any other key is sent as an HTTP header with that name. An optional `expires_in` key (seconds) tells the provider when to run the stack again. |

Leave out `provider_stack_file`, `provider_stack_name` and `output` to send no credentials (for a local development server).

How requests are sent:

| Stack | HTTP method | Variables travel in |
|---|---|---|
| `DATASOURCE`, `READ`, `RECREATE_ON`, `OUTPUT_CHANGES_ON` | `GET` | query string |
| `DELETE` | `DELETE` | query string |
| `CREATE`, `OPEN`, and action stacks | `POST` | JSON body |
| `UPDATE` | `PUT` | JSON body |

Redirects are never followed, so a login redirect from an auth proxy is reported as an authentication failure.

## Resource: `opsy_seristack`

Manages one resource whose lifecycle is defined by stacks.

```hcl
resource "opsy_seristack" "bucket" {
  type = "oci_bucket"

  vars = {
    region         = "ap-mumbai-1"
    namespace      = "mynamespace"
    name           = "my-bucket"
    compartment_id = var.compartment_id
  }

  sensitive = {
    # write-only: merged into the stack variables, never stored in state
    api_token = var.api_token
  }

  timeouts = {
    create = "30m"
    delete = "30m"
  }
}

output "bucket_etag" {
  value = opsy_seristack.bucket.output.etag
}
```

| Attribute | Type | Description |
|---|---|---|
| `type` | string, required | Resource type. Local mode: the file `<type>.yaml`. HTTP mode: the prefix of the endpoints. Changing it forces replacement. |
| `vars` | map(string), optional | Variables passed to every stack call. Values must be strings; use `jsonencode()` for lists and objects. |
| `sensitive` | map(string), optional, write-only | Secrets merged on top of `vars` at run time. Never stored in state or plan. |
| `timeouts` | object, optional | `create`, `read`, `update`, `delete`: Go durations. Default `10m` each. The limit covers the whole operation, including waiting for a long-running one. |
| `id` | string, computed | The `id` returned by the `CREATE` stack. |
| `output` | dynamic, computed | The `output` object returned by the last stack run. |

### Import

```shell
terraform import opsy_seristack.bucket '{"id":"mynamespace/my-bucket","type":"oci_bucket","vars":{"region":"ap-mumbai-1","namespace":"mynamespace","name":"my-bucket"}}'
```

The import ID is JSON with `id` and `type`, and an optional `vars` object. The provider runs the `READ` stack with them.

## Data source: `opsy_seristack`

Runs the `DATASOURCE` stack of a type and exposes its `id` and `output` for use elsewhere in the configuration. A stack for a data source only reads and never changes anything.

## Ephemeral resource: `opsy_seristack`

Runs the `OPEN` stack and returns its `output`. Nothing is stored in state or plan files, so it is the right way to fetch a secret and hand it to a write-only attribute.

```hcl
ephemeral "opsy_seristack" "db_password" {
  type = "oci_vault_secret"
  vars = {
    secret_id = var.secret_id
    region    = "ap-mumbai-1"
  }
}

resource "opsy_seristack" "database" {
  type = "my_database"
  vars = { name = "orders" }
  sensitive = {
    password = ephemeral.opsy_seristack.db_password.output.value
  }
}
```

`OPEN` is always a single synchronous call and is never polled.

## Action: `opsy_seristack`

Runs any named stack on demand, for example a restart, a backup or a cache flush.

```hcl
action "opsy_seristack" "restart" {
  config {
    type      = "my_service"
    stackname = "RESTART"
    vars      = { name = "orders" }
    timeout   = "20m"     # optional, default 30m
  }
}
```

Invoke it with `terraform apply -invoke=action.opsy_seristack.restart`. Progress messages are shown while the stack runs. Over HTTP the stack is called as `POST <url>/<type>_<stackname>` and must be registered with `method: POST`.

## The stack contract

A stack prints **one JSON object** on standard output. Fail a stack by exiting non-zero and writing the reason to standard error.

| Stack | Called for | Must print |
|---|---|---|
| `CREATE` | `terraform apply` creating the resource | `{"id": "...", "output": {...}}`. The `id` is required. |
| `READ` | refresh and import | `{"id": "...", "output": {...}}`. Print `{"not_found": true}` if the resource is gone: it is then removed from state. Optionally add a top-level `"vars": {...}` with the actual values of managed variables that drifted, so Terraform plans them back. |
| `UPDATE` | an in-place change of `vars` | `{"id": "...", "output": {...}}` |
| `DELETE` | destroy | any JSON object. It should succeed if the resource is already gone. |
| `RECREATE_ON` | plan, when `vars` changed | a JSON array of variable names (`[]` if none). If one of them changed, the resource is replaced instead of updated. If the stack is missing or fails, the plan shows a warning and the change is applied as an in-place update. |
| `OUTPUT_CHANGES_ON` | plan, when `vars` changed (optional) | a JSON array of top-level `output` keys that an update may change. Only those keys are shown as "known after apply". |
| `DATASOURCE` | the data source | `{"id": "...", "output": {...}}` |
| `OPEN` | the ephemeral resource | `{"output": {...}}` |

`READ`, `UPDATE` and `DELETE` receive the resource `id` plus all variables. `RECREATE_ON` and `OUTPUT_CHANGES_ON` receive the planned variables. In local mode, `READ`, `UPDATE` and `DELETE` also receive the previous output as `_output`.

Declare each variable in the stack YAML with `allowed_value` or `allowed_regex`; seristack rejects values that do not match before your commands run.

Variable names starting with `seristack_` are reserved by the provider. In the resource, setting one in `vars` or `sensitive` fails at `terraform validate`; in an action it fails when the action runs.

### Example stack

```yaml
stacks:
- name: oci_bucket_CREATE        # http mode: <type>_<STACK>; local mode: CREATE
  count: 1
  method: POST
  urlPath: /oci/oci_bucket_CREATE
  vars:
  - name: seristack_status      # set by the provider: "null" on the first call
    value: "null"
    allowed_value: ["null", "pending"]
  - name: region
    allowed_value: ["ap-mumbai-1"]
  - name: namespace
    allowed_regex: regex("^[a-z0-9]{1,64}$")
  - name: name
    allowed_regex: regex("^[a-zA-Z0-9._-]{1,256}$")
  cmds:
  - |
    created="$(oci os bucket create --region {{.Vars.region}} --namespace {{.Vars.namespace}} --name {{.Vars.name}})" || exit 1
    jq -n --arg id "{{.Vars.namespace}}/{{.Vars.name}}" --argjson r "$created" \
      '{id: $id, output: {etag: $r.data.etag}}'
```

The `seristack_status` variable is declared here because the provider sends it on every `CREATE`, `UPDATE`, `DELETE` and action call (see [Long-running operations](#long-running-operations)).

## Long-running operations

Many clouds accept a request and finish the work later. A `CREATE`, `UPDATE` or `DELETE` stack (and an action stack) can tell the provider to wait and ask again, with no change to your Terraform code.

1. On the first call the provider sends the variable `seristack_status=null`.
2. If the work is not finished, the stack prints:

   ```json
   {
     "seristack_status": "pending",
     "id": "<the resource's unique id, if the cloud already assigned one>",
     "seristack_retry": "30s",
     "seristack_status_vars": {"wr": "<handle needed to check again>"}
   }
   ```

3. The provider waits `seristack_retry`, then calls the **same stack again** with `seristack_status=pending`, the `id`, and `seristack_status_vars` (as compact JSON). In this branch the stack must only check progress and never create anything.
4. The stack answers `pending` again, or `{"seristack_status": "succeeded", "id": "...", "output": {...}}`, or `{"seristack_status": "failed", "error": "why"}`.

| Reserved name | Direction | Meaning |
|---|---|---|
| `seristack_status` | var and reply | var: `null` on the first call, `pending` on status checks. Reply: `pending`, `succeeded` or `failed`. |
| `seristack_retry` | reply | Wait before the next check: `10s`, `5m`, `1h` or a number of seconds (between 2 seconds and 1 hour). |
| `seristack_status_vars` | both | Small JSON handle (up to 2048 bytes) returned by the stack and sent back on each check. Do not put secrets in it. |

Notes:

- A reply with **no** `seristack_status` is a normal synchronous reply, so existing stacks keep working. Stacks that the provider calls for `CREATE`, `UPDATE`, `DELETE` or an action should declare the `seristack_status` variable.
- The `id` should be the cloud's unique identifier (such as an OCID), never the name: names can repeat.
- For `CREATE`, the provider saves the `id` to state as soon as the first `pending` reply carries one. If the wait fails, times out or is interrupted, the resource is **tainted** instead of orphaned, so make `DELETE` succeed when the resource is already gone.
- The total wait is limited by the resource's `timeouts` (or the action's `timeout`). Keep each stack call short: set `timeouts:` on the stack in YAML, and use `seristack_retry` for the delay between checks.

## Security

- **Secrets:** put them in `sensitive` (write-only, never stored in state) or fetch them on the server inside the stack. Values in `vars` and `output` are stored in plaintext in the Terraform state, so use an encrypted remote backend.
- **Transport:** in HTTP mode `POST` and `PUT` variables travel in a JSON body, so values do not appear in URL access logs. `GET` and `DELETE` variables are in the query string; do not use them for secrets.
- **Server logs:** seristack v0.4.10 and later redacts variables marked sensitive in its YAML from errors and audit logs. Proxies in front of the server keep their own logs, so avoid logging bodies there.
- **Authentication:** the bearer token is sent in the `Authorization` header and is refreshed automatically. Prefer short-lived tokens and an interactive or device-code flow with MFA over stored passwords.
- **Least privilege:** the server executes the commands in your stacks. Run it as a dedicated, unprivileged user, and validate every variable with `allowed_value` / `allowed_regex`.
- **Plain HTTP:** the provider warns when credentials would be sent over `http://` to a non-local host.

## Development

```shell
git clone https://github.com/TechXploreLabs/terraform-provider-opsy
cd terraform-provider-opsy
go build ./...
go test ./...
go install .
```

To use a local build, add a development override to `~/.terraformrc` (or `terraform.rc` on Windows):

```hcl
provider_installation {
  dev_overrides {
    "TechXploreLabs/opsy" = "/path/to/your/GOPATH/bin"
  }
  direct {}
}
```

Documentation for the registry is generated with [tfplugindocs](https://github.com/hashicorp/terraform-plugin-docs): run `go generate ./...`.

## License

[MPL-2.0](LICENSE)