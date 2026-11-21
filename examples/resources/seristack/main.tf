terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}

variable "password" {
  type = string
  sensitive = true
}


provider "opsy" { 
  http {
    url = "http://localhost:8082/oci/"
    # url                 = "https://yourapi.com/oci/"
    # provider_stack_file = "${path.module}/auth.yaml"
    # provider_stack_name = "auth0"

    # vars = {
    #   AUTHO_DOMAIN = ""
    #   CLIENT_ID    = ""
    #   USERNAME     = "john@org.com"
    #   PASSWORD     = var.password
    #   AUDIENCE     = "https://yourapi.com"

    #   # optional, read by the provider itself:
    #   # timeout      = "10m"
    #   # ca_cert_file = "/path/to/ca.pem"
    # }

    # # Only access_token. Do NOT list expires_in here.
    # output = ["access_token"]
  }
}



ephemeral "opsy_seristack" "db_password" {
  type = "oci_vault_secret"
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
