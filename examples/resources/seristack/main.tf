terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}


provider "opsy" {
  local {
   # path = "${path.module}/archive.zip"
   folder_path = "${path.module}/opsyyamls"
  }
}


ephemeral "opsy_seristack" "db_password" {
  type = "oci_bucket"
  vars = {
    secret_id = "ocid1.vaultsecret.oc1.ap-mumbai-1.amaaaaaafkdjaoyaihidq44poxk56ar4id4utjgnw5lem6kyaz6jms3cylta"
    region    = "ap-mumbai-1"
    version_number  = "1"
  }
}


resource "opsy_seristack" "testing1" {
    type = "oci_bucket"
    vars = {
        compartment_id = "ocid1.compartment.oc1..aaaaaaaajqiinbe47xv6fkfvehntqkng6yy6bxes4zadf7uvbb4vfohnfqwa"
        namespace      = "bm0fmmqpaonp"
        name           = "seristack"
        storage_tier   = "Standard"
        region         = "ap-mumbai-1"
        freeform_tags_json = jsonencode({
          env   = "dev"
        })
    }
    sensitive = {
       db_password = ephemeral.opsy_seristack.db_password.output.value
     }
}

output "testing1" {
  value = opsy_seristack.testing1.output
}


# data "opsy_seristack" "testing1" {
#   type = "oci_bucket"
#   vars = {
#     namespace = "yournamespace"
#     name = "bucket"
#     region = "eu-frankfurt-1"
#   }
# }


# output "data-testing1" {
#   value = data.opsy_seristack.testing1
# }