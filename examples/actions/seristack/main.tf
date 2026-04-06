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


action "opsy_seristack" "action1" {
  config{
    type      = "function"
    stackname = "function_1"
    vars = {
      name      = "opsy"
    }
  }
}