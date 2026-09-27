config {
  # `module = true` was removed in TFLint v0.54; "all" is its equivalent
  # (inspect calls to local and remote modules).
  call_module_type    = "all"
  force               = false
  disabled_by_default = false
}

plugin "aws" {
  enabled = true
  version = "0.32.0"
  source  = "github.com/terraform-linters/tflint-ruleset-aws"
}
