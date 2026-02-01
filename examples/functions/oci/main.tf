output "service" {
 value = provider::opsy::oci("ocid1.tenancy.oc1..aaaaaaaabyyyyyyyyyyyyyyyyyyyyyyyyyq")["service"]
}

output "region" {
 value = provider::opsy::oci("ocid1.instance.oc1.iad.aaaaaaaabyyyyyyyyyyyyyyyyyyyyyyyyyq")["region"]
}