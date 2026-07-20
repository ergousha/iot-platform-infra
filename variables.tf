variable "aws_region" {
  description = "AWS region (choose a region supported by IoT Core)."
  type        = string
  default     = "eu-central-1"
}

variable "project_name" {
  description = "Prefix for resource naming."
  type        = string
  default     = "esp32-ztp"
}

variable "provisioning_template_name" {
  description = "Fleet Provisioning template name. Must match the firmware."
  type        = string
  default     = "esp32-s3-fleet-template"
}

variable "log_retention_days" {
  description = "CloudWatch log retention period (kept low for cost reasons)."
  type        = number
  default     = 7
}

variable "telemetry_topic_prefix" {
  description = "Telemetry topic prefix for devices to publish to."
  type        = string
  default     = "dt"
}

variable "github_repositories" {
  description = "Allowed GitHub repositories with their numeric IDs"
  type = list(object({
    name          = string
    repository_id = number
  }))
  default = [
    {
      name          = "ergousha/esp32-opcua-gateway-rust"
      repository_id = 1302001123
    },
    {
      name          = "ergousha/esp32-display-vaktus-salat-rust"
      repository_id = 1290322529
    }
  ]
}

variable "github_owner_id" {
  description = "Permanent numeric ID of the GitHub repository owner."
  type        = number
  default     = 2912014
}
