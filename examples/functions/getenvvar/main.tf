output "local_var" {
  value = provider::opsy::getenvvar("TF_VAR_password")
}

terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}