# ---------------------------------------------------------------------------
# OTA Trigger Lambda (serverless, event-driven).
# ---------------------------------------------------------------------------
resource "terraform_data" "build_ota_trigger" {
  triggers_replace = {
    source_hash = sha256(join("", [
      filesha256("${path.module}/lambda/ota_trigger/main.go"),
    ]))
  }

  provisioner "local-exec" {
    command = "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' -o ${path.module}/lambda/ota_trigger/bootstrap ${path.module}/lambda/ota_trigger/main.go"
  }
}

data "archive_file" "ota_trigger_zip" {
  type        = "zip"
  source_file = "${path.module}/lambda/ota_trigger/bootstrap"
  output_path = "${path.module}/.build/ota_trigger.zip"
  depends_on  = [terraform_data.build_ota_trigger]
}

resource "aws_cloudwatch_log_group" "ota_trigger" {
  name              = "/aws/lambda/${var.project_name}-ota-trigger"
  retention_in_days = var.log_retention_days
}

resource "aws_iam_role" "ota_trigger" {
  name = "${var.project_name}-ota-trigger-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "lambda.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy" "ota_trigger" {
  name = "${var.project_name}-ota-trigger-policy"
  role = aws_iam_role.ota_trigger.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "${aws_cloudwatch_log_group.ota_trigger.arn}:*"
      },
      {
        Effect = "Allow"
        Action = [
          "iot:ListThings",
          "iot:CreateJob"
        ]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject"
        ]
        Resource = "${aws_s3_bucket.firmware.arn}/*"
      }
    ]
  })
}

resource "aws_lambda_function" "ota_trigger" {
  function_name    = "${var.project_name}-ota-trigger"
  role             = aws_iam_role.ota_trigger.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  filename         = data.archive_file.ota_trigger_zip.output_path
  source_code_hash = data.archive_file.ota_trigger_zip.output_base64sha256

  memory_size = 128
  timeout     = 30

  environment {
    variables = {
      S3_BUCKET_NAME = aws_s3_bucket.firmware.bucket
    }
  }

  depends_on = [aws_cloudwatch_log_group.ota_trigger]
}

resource "aws_lambda_permission" "allow_s3" {
  statement_id  = "AllowExecutionFromS3Bucket"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.ota_trigger.function_name
  principal     = "s3.amazonaws.com"
  source_arn    = aws_s3_bucket.firmware.arn
}

resource "aws_s3_bucket_notification" "bucket_notification" {
  bucket = aws_s3_bucket.firmware.id

  lambda_function {
    lambda_function_arn = aws_lambda_function.ota_trigger.arn
    events              = ["s3:ObjectCreated:*"]
    filter_suffix       = ".bin"
  }

  depends_on = [aws_lambda_permission.allow_s3]
}
