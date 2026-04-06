terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}

# Private bucket/object example:
# - scripts.zip is private
# - provider sends credential as HTTP Authorization
#   Supported values:
#     Bearer <token>
#     Basic <base64>
#     user:pass
#     <token> (treated as Bearer)
provider "opsy" {
  storage {
    type   = "oci"
    region = "us-phoenix-1"

    namespace = "your_namespace"
    bucket    = "private-opsy-bucket"
    prefix    = "artifacts/"

    # Keep secret in env var or tfvars, avoid hardcoding in git.
    credential = var.opsy_storage_credential
  }
}

variable "opsy_storage_credential" {
  description = "HTTP auth credential for private object access"
  type        = string
  sensitive   = true
}
