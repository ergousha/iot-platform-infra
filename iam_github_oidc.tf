# ---------------------------------------------------------------------------
# GitHub OIDC Provider and Role
# ---------------------------------------------------------------------------

locals {
  github_oidc_subs = [
    for repo in var.github_repositories :
    "repo:${split("/", repo.name)[0]}@${var.github_owner_id}/${split("/", repo.name)[1]}@${repo.repository_id}:ref:refs/heads/main"
  ]
}

resource "aws_iam_openid_connect_provider" "github" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1", "1c58a3a8518e8759bf075b76b750d4f2df264fcd"] # GitHub's standard thumbprints
}

data "aws_iam_policy_document" "github_assume_role" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = local.github_oidc_subs
    }
  }
}

resource "aws_iam_role" "github_actions" {
  name               = "${var.project_name}-github-actions-role"
  assume_role_policy = data.aws_iam_policy_document.github_assume_role.json
}

data "aws_iam_policy_document" "github_actions_policy" {
  statement {
    actions = [
      "s3:PutObject"
    ]
    resources = [
      "${aws_s3_bucket.firmware.arn}/*"
    ]
  }

  # The firmware release build compiles the IoT endpoint, the provisioning
  # template and the claim identity into the image; its build.rs reads them
  # from these parameters. Without this, CI built an image with an empty
  # endpoint and a placeholder claim identity. Read-only, and exactly these
  # four. The SecureStrings use the AWS managed aws/ssm key, whose key policy
  # already allows decryption through SSM for principals in this account.
  statement {
    actions = ["ssm:GetParameter"]
    resources = [
      aws_ssm_parameter.iot_endpoint.arn,
      aws_ssm_parameter.provisioning_template.arn,
      aws_ssm_parameter.claim_certificate.arn,
      aws_ssm_parameter.claim_private_key.arn,
    ]
  }
}

resource "aws_iam_role_policy" "github_actions_s3" {
  name   = "${var.project_name}-github-actions-s3-policy"
  role   = aws_iam_role.github_actions.id
  policy = data.aws_iam_policy_document.github_actions_policy.json
}
