# Terraform Provider Opsy

To use provider functions, declare the provider as a required provider in your Terraform configuration:

```hcl
terraform {
  required_providers {
    opsy = {
      source = "TechXploreLabs/opsy"
    }
  }
}
```

## Resource Condition

Simplify time based resource condition that run as part of your Terraform workflow:

```hcl
resource "aws_instance" "test" {
  ami           = "ami-0abcdef1234567890"
  instance_type = "t3.micro"

  tags = {
    Name = "HelloWorld"
  }
}

resource "aws_ec2_instance_state" "test" {
  instance_id = aws_instance.test.id
  state       = provider::opsy::timecheck(["ALL"], ["January"], "America/New_York", "09:00", "17:00") ? "running" : "stopped"
}
```

## Variable Validation

Write simple validation rules for your Terraform variables:

```hcl
variable "time" {
  type        = bool
  description = "Check whether the current time matches the provider time slot"
  validation {
    condition     = var.time == provider::opsy::timecheck(["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"], ["ALL"], "America/New_York", "09:00", "17:00")
    error_message = "true"
  }
  default = true
}
```