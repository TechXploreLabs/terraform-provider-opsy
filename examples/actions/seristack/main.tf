
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


action "opsy_seristack" "action1" {
  config {
    type      = "function"
    vars = {
      duration_seconds = "180"   # how long the demo runs
      retry_seconds    = "30"    # how often the provider checks again
    }
    timeout = "10m"              # must be longer than duration_seconds
  }
}
