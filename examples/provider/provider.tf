terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}

# seristack provider yaml file folder path
provider "opsy" {
  alias = "yaml_files"
  local {
    folder_path = "${path.module}/opsyyamls"
  }
}


# seristack provider code zip file
provider "opsy" {
  alias = "zip_file"
  local {
    path = "${path.module}/opsy.zip"
  }
}


# seristack provider local http server
provider "opsy" {
  alias = "local_http"
  http {
    url = "http://localhost:8080"
  }
}

# seristack provider remote https api server
provider "opsy" {
  alias = "remote_https"
  http {
    url                 = "<endpoint_url>"
    provider_stack_file = "${path.module}/auth.yaml"
    provider_stack_name = "auth0"
    vars = {
      # AUTHO_DOMAIN = ""
      # CLIENT_ID    = ""
      # USERNAME     = "john@gmail.com"
      # AUDIENCE     = "audience_url"

      # optional, read by the provider itself:
      # timeout      = "10m"
      # ca_cert_file = "/path/to/ca.pem"
    }

    # Only access_token. Do NOT list expires_in here.
    output = ["access_token"]
  }
}