---
page_title: "Provider: Opsy"
description: |-
  The Opsy provider provides functions to verify values in your Terraform configurations to make sure they meet specific criteria.
---

# Opsy Provider

The Opsy provider for Terraform is a operational support provider that offers supports through provider-defined functions.

## Example Usage

As of Terraform 1.8 and later, providers can implement functions that you can call from the Terraform configuration. 

To use the Opsy provider, declare it as a `required_provider` in the `terraform {}` block:

```terraform
terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}
```

## Function Syntax

You use the functions with a special syntax: `provider::opsy::<function_name>`. 