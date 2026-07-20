# ---------------------------------------------------------------------------
# SSM Parameter Store integration
# Stores the outputs required by the firmware build system.
# ---------------------------------------------------------------------------

resource "aws_ssm_parameter" "iot_endpoint" {
  name        = "/esp32-ztp/poc/iot_endpoint"
  type        = "String"
  value       = data.aws_iot_endpoint.ats.endpoint_address
  description = "AWS IoT Core endpoint for firmware"
}

resource "aws_ssm_parameter" "provisioning_template" {
  name        = "/esp32-ztp/poc/provisioning_template_name"
  type        = "String"
  value       = aws_iot_provisioning_template.fleet.name
  description = "Fleet provisioning template name"
}

resource "aws_ssm_parameter" "claim_certificate" {
  name        = "/esp32-ztp/poc/claim_certificate_pem"
  type        = "SecureString"
  value       = aws_iot_certificate.claim.certificate_pem
  description = "Bootstrap claim certificate"
}

resource "aws_ssm_parameter" "claim_private_key" {
  name        = "/esp32-ztp/poc/claim_private_key"
  type        = "SecureString"
  value       = aws_iot_certificate.claim.private_key
  description = "Bootstrap claim private key"
}
