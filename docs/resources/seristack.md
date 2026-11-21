---
page_title: "opsy_seristack Resource - opsy"
subcategory: ""
description: |-
  Manages a resource lifecycle driven by seristack stacks.
---

# opsy_seristack (Resource)

Manages infrastructure using seristack stacks. Stacks run either locally (provider `local` block) or on a seristack HTTP server (provider `http` block).

The `type` value selects the stack definition:

- **local mode:** it maps to `<type>.yaml` (or `.yml`) inside the configured bundle (archive.zip) or folder. For example, `type = "oci_bucket"` resolves `oci_bucket.yaml`.
- **http mode:** it is the prefix of the endpoints: `<url>/<type>_CREATE`, `<url>/<type>_READ`, and so on.

Resource lifecycle maps to seristack stack names:

- `CREATE`
- `READ`
- `UPDATE`
- `DELETE`
- `RECREATE_ON` (optional, see below)
- `OUTPUT_CHANGES_ON` (optional, see below)

To know more about seristack, please visit [seristack](https://seristack.getsaas.in/).

## Example Usage

```terraform
terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}

provider "opsy" {
  local {
    folder_path = "${path.module}/opsyyamls"
  }
}

resource "opsy_seristack" "bucket" {
  type = "oci_bucket"
  vars = {
    compartment_id = "ocid1.tenancy..."
    namespace      = "namespacename"
    name           = "opsy-testing"
    storage_tier   = "Standard"
  }

  # Write-only: merged into the stack variables, never stored in state.
  # sensitive = {
  #   api_token = var.api_token
  # }

  timeouts = {
    create = "30m"
    delete = "30m"
  }
}

# `output` is a structured value: read its keys directly.
output "bucket_etag" {
  value = opsy_seristack.bucket.output.etag
}

output "bucket_output" {
  value = opsy_seristack.bucket.output
}
```

## Stack contract

A stack prints **one JSON object** on standard output. A stack fails by exiting non-zero and writing the reason to standard error.

| Stack | Called for | Must print |
|---|---|---|
| `CREATE` | creating the resource | `{"id": "...", "output": {...}}`. The `id` is required. |
| `READ` | refresh and import | `{"id": "...", "output": {...}}`. Print `{"not_found": true}` if the resource is gone: it is then removed from state. Optionally add a top-level `"vars": {...}` with the actual values of managed variables that drifted, so Terraform plans them back. |
| `UPDATE` | an in-place change of `vars` | `{"id": "...", "output": {...}}` |
| `DELETE` | destroy | any JSON object. It should succeed if the resource is already gone. |
| `RECREATE_ON` | plan, when `vars` changed | a JSON array of variable names (`[]` if none). If one of them changed, the resource is replaced instead of updated. If the stack is missing or fails, the plan shows a warning and the change is applied as an in-place update. |
| `OUTPUT_CHANGES_ON` | plan, when `vars` changed (optional) | a JSON array of top-level `output` keys that an update may change. Only those keys are shown as "known after apply". |

`READ`, `UPDATE` and `DELETE` receive the resource `id` plus all variables. `RECREATE_ON` and `OUTPUT_CHANGES_ON` receive the planned variables. In local mode, `READ`, `UPDATE` and `DELETE` also receive the previous output as `_output`.

Declare each variable in the stack YAML with `allowed_value` or `allowed_regex`; seristack rejects values that do not match before your commands run.

Variable names starting with `seristack_` are reserved by the provider. Setting one in `vars` or `sensitive` fails at `terraform validate`.

## Long-running operations

A `CREATE`, `UPDATE` or `DELETE` stack can tell the provider to wait and ask again, with no change to your Terraform code.

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
| `seristack_status` | variable and reply | variable: `null` on the first call, `pending` on status checks. Reply: `pending`, `succeeded` or `failed`. |
| `seristack_retry` | reply | Wait before the next check: `10s`, `5m`, `1h` or a number of seconds (between 2 seconds and 1 hour). |
| `seristack_status_vars` | both | A small JSON handle (up to 2048 bytes) returned by the stack and sent back on each check. Do not put secrets in it. |

- A reply with no `seristack_status` is a normal synchronous reply, so existing stacks keep working. Stacks that the provider calls for `CREATE`, `UPDATE` or `DELETE` should declare the `seristack_status` variable.
- The `id` should be the cloud's unique identifier (such as an OCID), never the name: names can repeat.
- For `CREATE`, the provider saves the `id` to state as soon as the first `pending` reply carries one. If the wait fails, times out or is interrupted, the resource is **tainted** instead of orphaned, so make `DELETE` succeed when the resource is already gone.
- The total wait is limited by `timeouts`. Keep each stack call short: set `timeouts:` on the stack in YAML, and use `seristack_retry` for the delay between checks.

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `type` (String) Resource type name. With the `local` block it maps to `<type>.yaml`; with the `http` block it is the prefix of the stack endpoints (`/<type>_CREATE`, ...). Changing the type always forces replacement.

### Optional

- `sensitive` (Map of String, Sensitive, Write-only) Write-only map of sensitive variables (tokens, passwords, keys). Merged on top of `vars` at execution time. Never stored in state.
- `timeouts` (Attributes) (see [below for nested schema](#nestedatt--timeouts))
- `vars` (Map of String) Variables passed to every stack invocation. Values must be strings; use `jsonencode()` for lists and objects. Names starting with `seristack_` are reserved.

### Read-Only

- `id` (String) Resource identifier returned by the create stack.
- `output` (Dynamic) Structured output returned by the last stack execution.

<a id="nestedatt--timeouts"></a>
### Nested Schema for `timeouts`

Optional:

- `create` (String) A string that can be [parsed as a duration](https://pkg.go.dev/time#ParseDuration) consisting of numbers and unit suffixes, such as "30s" or "2h45m". Valid time units are "s" (seconds), "m" (minutes), "h" (hours). Default `10m`.
- `delete` (String) A string that can be [parsed as a duration](https://pkg.go.dev/time#ParseDuration) consisting of numbers and unit suffixes, such as "30s" or "2h45m". Valid time units are "s" (seconds), "m" (minutes), "h" (hours). Setting a timeout for a Delete operation is only applicable if changes are saved into state before the destroy operation occurs. Default `10m`.
- `read` (String) A string that can be [parsed as a duration](https://pkg.go.dev/time#ParseDuration) consisting of numbers and unit suffixes, such as "30s" or "2h45m". Valid time units are "s" (seconds), "m" (minutes), "h" (hours). Read operations occur during any refresh or planning operation when refresh is enabled. Default `10m`.
- `update` (String) A string that can be [parsed as a duration](https://pkg.go.dev/time#ParseDuration) consisting of numbers and unit suffixes, such as "30s" or "2h45m". Valid time units are "s" (seconds), "m" (minutes), "h" (hours). Default `10m`.

## Import

Import is supported using the following syntax. The import ID is a JSON string with `id` and `type`, and an optional `vars` object. The provider runs the `READ` stack with them.

```shell
terraform import opsy_seristack.bucket '{"id":"namespacename/opsy-testing","type":"oci_bucket","vars":{"namespace":"namespacename","name":"opsy-testing"}}'
```

With an `import` block:

```terraform
import {
  to = opsy_seristack.bucket
  id = jsonencode({
    id   = "namespacename/opsy-testing"
    type = "oci_bucket"
    vars = {
      namespace = "namespacename"
      name      = "opsy-testing"
    }
  })
}
```