# Example: `opsy_seristack` resource over HTTP

This example runs the `opsy_seristack` resource against a **local seristack HTTP server**. Terraform sends each lifecycle call (`CREATE`, `READ`, `UPDATE`, `DELETE`) to the server, and the server runs the matching stack from `provider.yaml`.

The example manages an OCI Object Storage bucket (`type = "oci_bucket"`).

## Prerequisites

- The tools your stacks call. For this example: the [OCI CLI](https://docs.oracle.com/en-us/iaas/Content/API/Concepts/cliconcepts.htm) with credentials configured, and `jq`
- A `provider.yaml` that defines the stacks, named `oci_bucket_CREATE`, `oci_bucket_READ`, `oci_bucket_UPDATE`, `oci_bucket_DELETE` and so on, each with a `urlPath` such as `/oci/oci_bucket_CREATE`

## 1. Install seristack

### macOS and Linux (Homebrew)

```bash
brew install TechXploreLabs/tap/seristack
```

### Linux

```bash
curl -fsSL https://raw.githubusercontent.com/TechXploreLabs/seristack/main/install.sh | bash
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/TechXploreLabs/seristack/main/install.ps1 | iex
```

Check the install:

```bash
seristack --help
```

## 2. Start the local HTTP server

```bash
seristack run -c provider.yaml -p 8082
```

Leave it running in this terminal. On start it prints one line per stack, for example `Registered: POST /oci/oci_bucket_CREATE`.


The values in `vars` must satisfy the `allowed_value` / `allowed_regex` rules declared for each variable in `provider.yaml`. Otherwise the server rejects the call.

## 4. Run Terraform

In a **second terminal**, in the folder with `main.tf`:

```bash
terraform init
terraform plan
terraform apply
```

## 5. Clean up

```bash
terraform destroy
```

## IMPORT

Below import block is the recommended apprach to import existing resource in tfstate

```hcl
import {
  id = jsonencode(  {  
    id   = "bm0fmmqpaonp/seristack"
    type = "oci_bucket"
    vars = {
        compartment_id = "ocid1.compartment.oc1..aaaaaa"
        namespace      = "bm0fmmqpaonp"
        name           = "seristack"
        storage_tier   = "Standard"
        region         = "ap-mumbai-1"
        freeform_tags_json = jsonencode({
          env   = "dev"
          mode = "http"
        })
    }})
  to = opsy_seristack.testing1
}
```


Then stop the server with `Ctrl+C`.

Use `-auto-approve` only for throwaway environments or CI.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `405 Not Allowed` with an HTML page | The `url` points at a web server, not the seristack server. Check the host and port. |
| `404` | The `url` prefix does not match the stack paths. Compare it with the `Registered:` lines. |
| `connection refused` | The server is not running, or the port in `url` differs from `-p`. |
| Error about a variable value | The value does not match its `allowed_value` / `allowed_regex` in `provider.yaml`. |

To see which URL the provider calls, run Terraform with `TF_LOG_PROVIDER=DEBUG` and look for the `calling stack` lines.