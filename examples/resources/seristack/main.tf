terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}


provider "opsy" {
  # local {
  #    folder_path = "${path.module}/opsyyamls"
  #   # path = "${path.module}/archive.zip"
  # }

  http {
    # url = "http://localhost:8082/oci/"
    url                 = "https://seristackapi.getsaas.in/oci/"
    provider_stack_file = "${path.module}/auth.yaml"
    provider_stack_name = "auth0"

    vars = {


      # optional, read by the provider itself:
      # timeout      = "10m"
      # ca_cert_file = "/path/to/ca.pem"
    }

    # Only access_token. Do NOT list expires_in here.
    output = ["access_token"]
  }
}


# ephemeral "opsy_seristack" "db_password" {
#   type = "oci_bucket"
#   vars = {
#     secret_id = "ocid1.vaultsecret.oc1.ap-mumbai-1.amaaaaaafkdjaoyaihidq44poxk56ar4id4utjgnw5lem6kyaz6jms3cylta"
#     region    = "ap-mumbai-1"
#     version_number  = "1"
#   }
# }


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
          mode = "http"
        })
    }
    # sensitive = {
    #    db_password = ephemeral.opsy_seristack.db_password.output.value
    #  }
}

output "testing1" {
  value = opsy_seristack.testing1.output
}


# data "opsy_seristack" "testing1" {
#   type = "oci_bucket"
#   vars = {
#     namespace = "bm0fmmqpaonp"
#     name = "bucket"
#     region = "ap-mumbai-1"
#   }
# }


# output "data-testing1" {
#   value = data.opsy_seristack.testing1
# }