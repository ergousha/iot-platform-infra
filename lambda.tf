# ---------------------------------------------------------------------------
# Pre-provisioning hook Lambda (serverless, on-demand).
# ---------------------------------------------------------------------------
resource "terraform_data" "build_hook" {
  triggers_replace = {
    source_hash = sha256(join("", [
      filesha256("${path.module}/lambda/pre_provisioning_hook/main.go"),
    ]))
  }

  provisioner "local-exec" {
    command = "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' -o ${path.module}/lambda/pre_provisioning_hook/bootstrap ${path.module}/lambda/pre_provisioning_hook/main.go"
  }
}

data "archive_file" "hook_zip" {
  type        = "zip"
  source_file = "${path.module}/lambda/pre_provisioning_hook/bootstrap"
  output_path = "${path.module}/.build/pre_provisioning_hook.zip"
  depends_on  = [terraform_data.build_hook]
}

resource "aws_cloudwatch_log_group" "hook" {
  name              = "/aws/lambda/${var.project_name}-pre-provisioning-hook"
  retention_in_days = var.log_retention_days
}

resource "aws_lambda_function" "pre_provisioning_hook" {
  function_name    = "${var.project_name}-pre-provisioning-hook"
  role             = aws_iam_role.hook.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  filename         = data.archive_file.hook_zip.output_path
  source_code_hash = data.archive_file.hook_zip.output_base64sha256

  memory_size = 128 # minimum; maliyet-etkin
  timeout     = 5   # IoT hook zaten ~5sn bekler

  environment {
    variables = {
      DEVICE_TABLE = aws_dynamodb_table.device_registry.name
    }
  }

  depends_on = [aws_cloudwatch_log_group.hook]
}

# IoT Core'un bu Lambda'yi cagirmasina izin ver.
# source_arn'i locals'tan (string) veriyoruz; template resource'una referans
# vermek dongu yaratirdi. Boylece izin, template'ten once olusur.
resource "aws_lambda_permission" "allow_iot" {
  statement_id  = "AllowIoTFleetProvisioningInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.pre_provisioning_hook.function_name
  principal     = "iot.amazonaws.com"
  source_arn    = local.provisioning_template_arn
}
