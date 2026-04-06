terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}

# Public bucket/object example:
# - scripts.zip is publicly readable
# - no credential is required
provider "opsy" {
  storage {
    type   = "oci"
    region = "us-phoenix-1"

    namespace = "your_namespace"
    bucket    = "public-opsy-bucket"
    prefix    = "artifacts/"
  }
}
