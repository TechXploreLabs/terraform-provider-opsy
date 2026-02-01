
---
page_title: "oci function - terraform-provider-opsy"
subcategory: "OCI Functions"
description: |-
  Parse the OCI ocid
---

# function: oci



The oci function `oci` parse the ocid and results back the service and region name.

## Output Example

```terraform
output "service" {
 value = provider::opsy::oci("ocid1.tenancy.oc1..aaaaaaaabyyyyyyyyyyyyyyyyyyyyyyyyyq")["service"]
}

output "region" {
 value = provider::opsy::oci("ocid1.instance.oc1.iad.aaaaaaaabyyyyyyyyyyyyyyyyyyyyyyyyyq")["region"]
}
```

