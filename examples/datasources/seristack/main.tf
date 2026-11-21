

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

data "opsy_seristack" "data" {
    type = "oci_bucket"
    vars = {
      namespace = "namespacename"
      name = "bucket-compartment-id"
    }
}

output "data_opsy" {
  value = jsondecode(data.opsy_seristack.data.output)
}
