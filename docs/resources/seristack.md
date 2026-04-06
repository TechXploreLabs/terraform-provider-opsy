---
page_title: "opsy_seristack Resource - opsy"
subcategory: ""
description: |-
  Manages a resource lifecycle driven by a seristack YAML definition.
---

# opsy_seristack (Resource)

Manages infrastructure using seristack stacks loaded from the provider zip bundle.

The `type` value maps to `<type>.yaml` (or `.yml`) inside the configured bundle.
For example, `type = "oci_bucket"` resolves `oci_bucket.yaml`.

Resource lifecycle maps to seristack stack names:

- `CREATE`
- `READ`
- `UPDATE`
- `DELETE`

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
    path = "${path.module}/archive.zip"
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
}

output "bucket_output" {
  value = jsondecode(opsy_seristack.bucket.output)
}
```

## Schema

### Required

- `type` (String) Resource type name. Maps to `<type>.yaml` inside the zip bundle.

### Optional

- `vars` (Map of String) Variables passed to every stack invocation.
- `sensitive` (Map of String, Sensitive, Write-only) Sensitive values merged on top of `vars` at execution time and not stored in state.

### Read-Only

- `id` (String) Resource identifier returned by the create stack.
- `output` (String) JSON string of the output returned by the last stack execution.
