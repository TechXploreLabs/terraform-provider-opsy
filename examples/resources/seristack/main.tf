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
        compartment_id = "ocid1.compartment.oc1.."
        namespace      = "yournamespace"
        name           = "bucketname"
        storage_tier   = "Standard"
        region         = "eu-frankfurt-1"
        freeform_tags_json = jsonencode({
          env   = "dev"
          owner = "john"
        })
    }
}

output "testing1" {
  value = jsondecode(opsy_seristack.testing1.output)
}


data "opsy_seristack" "testing1" {
  type = "oci_bucket"
  vars = {
    namespace = "yournamespace"
    name = "bucket"
    region = "eu-frankfurt-1"
  }
}


output "data-testing1" {
  value = data.opsy_seristack.testing1
}