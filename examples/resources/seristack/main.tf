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


resource "opsy_seristack" "testing1" {
    type = "oci_bucket"
    vars = {
        compartment_id = "ocid1.tenancy."
        namespace      = "namespacename"
        name           = "opsy-testing"
        storage_tier   = "Standard"
        freeform_tags_json = jsonencode({
          env   = "prod"
          owner = "opsy"
        })
    }
}

output "testing1" {
  value = jsondecode(opsy_seristack.testing1.output)
}


data "opsy_seristack" "testing1" {
  type = "oci_bucket"
  vars = {
    namespace = "namespacename"
    name = "bucket-compartment-id"
  }
}


output "data-testing1" {
  value = data.opsy_seristack.testing1
}