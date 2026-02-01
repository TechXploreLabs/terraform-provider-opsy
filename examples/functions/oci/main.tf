output "service" {
  value = provider::opsy::oci("ocid1.tenancy.oc1..aaaaaaaabyyyyyyyyyyyyyyyyyyyyyyyyyq")
}

output "region" {
  value = provider::opsy::oci("ocid1.instance.oc1.iad.aaaaaaaabyyyyyyyyyyyyyyyyyyyyyyyyyq")
}
