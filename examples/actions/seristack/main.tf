action "opsy_seristack" "action1" {
  config {
    configfile = "${path.module}/seristack.yaml"
    stackname  = "welcome"
    vars = {
      msg = "hello"
    }
  }
}

action "opsy_seristack" "action2" {
  config {
    configfile = "${path.module}/seristack.yaml"
    stackname  = "bye"
    vars = {
      msg = "hello"
    }
  }
}