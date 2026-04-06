---
page_title: "opsy_seristack Data Source - opsy"
subcategory: ""
description: |-
  Reads data via a seristack YAML definition.
---

# opsy_seristack (Data Source)

Reads data by executing the `DATASOURCE` stack from a seristack type definition in the provider zip bundle.

The `type` value maps to `<type>.yaml` (or `.yml`) inside the configured bundle.
For example, `type = "oci_bucket"` resolves `oci_bucket.yaml`.

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

data "opsy_seristack" "bucket" {
  type = "oci_bucket"
  vars = {
    namespace = "namespacename"
    name      = "bucket-compartment-id"
  }
}

output "bucket_data" {
  value = jsondecode(data.opsy_seristack.bucket.output)
}
```

## Schema

### Required

- `type` (String) Datasource type name. Maps to `<type>.yaml` inside the zip bundle.

### Optional

- `vars` (Map of String) Variables to pass to the stack.

### Read-Only

- `id` (String) Identifier derived from stack output or stack name.
- `output` (String) Raw JSON string output from the stack execution.
