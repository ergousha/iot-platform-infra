# ESP32 Zero-Touch Provisioning (ZTP) & OTA Platform Infrastructure

This repository contains the Terraform-managed AWS serverless infrastructure that powers **Zero-Touch Fleet Provisioning by Claim (ZTP)** and **Automated, Event-Driven Over-The-Air (OTA) Firmware Updates** for ESP32 devices.

---

## 1. High-Level Solution Architecture

This diagram shows the relationship between developers, GitHub Actions, AWS infrastructure components, and the ESP32 devices:

```mermaid
graph TD
    subgraph GitHub ["CI/CD Pipeline"]
        Dev[Developer] -->|Git Push / Release| GH[GitHub Actions]
    end

    subgraph AWS ["AWS IoT Cloud Infrastructure"]
        OIDC[IAM OIDC Provider] <-->|Authenticate| GH
        S3_Firmware[(S3 Firmware Bucket)]
        GH -->|Upload firmware.bin| S3_Firmware
        
        S3_State[(S3 TF State Bucket)]
        
        SSM[SSM Parameter Store]
        
        Lambda_OTA[Lambda: ota_trigger]
        S3_Firmware -->|S3 Event: ObjectCreated| Lambda_OTA
        
        IoT[AWS IoT Core]
        Lambda_OTA -->|Create Job| IoT
        
        Lambda_ZTP[Lambda: pre-provisioning-hook]
        IoT -->|Invoke Hook| Lambda_ZTP
        
        DB[(DynamoDB Device Registry)]
        Lambda_ZTP <-->|Verify MAC/Secret & Set Provisioned| DB
    end

    subgraph Device ["ESP32-S3 Firmware"]
        ESP[ESP32 Device]
    end

    ESP -->|1. ZTP: Claim Conn| IoT
    ESP -->|2. Get Unique Cert| IoT
    ESP -->|3. Reconnect with Device Cert| IoT
    ESP -->|4. Poll OTA Jobs| IoT
    ESP -->|5. Download Firmware| S3_Firmware
```

---

## 2. Zero-Touch Provisioning (ZTP) Flow

ZTP uses **AWS IoT Fleet Provisioning by Claim**. The device leaves the factory with a **common** "claim" (bootstrap) certificate. Upon first connection, it generates its own **unique** certificate, verifies its identity via a pre-provisioning hook, and receives unique credentials.

### ZTP Sequence Diagram

```mermaid
sequenceDiagram
    autonumber
    participant ESP32 as ESP32 Device
    participant IoT as AWS IoT Core
    participant Hook as Lambda (pre-provisioning-hook)
    participant Dynamo as DynamoDB Table

    ESP32->>IoT: TLS Connect using Fleet CLAIM Certificate
    ESP32->>IoT: Publish to $aws/certificates/create/json
    IoT-->>ESP32: Return unique Certificate, Private Key, and Register Token
    Note over ESP32: Temporarily store unique cert/key
    ESP32->>IoT: Publish to $aws/provisioning-templates/+/provision/json<br/>(Payload: Token, SerialNumber, MacAddress, Secret)
    IoT->>Hook: Invoke Pre-Provisioning Hook with parameters
    Hook->>Dynamo: Fetch record by MAC Address (consistent read)
    Dynamo-->>Hook: Return MAC, secret, allowed, and provisioned status
    Alt validation successful (allowed=true & secret matches)
        Hook->>Dynamo: Update status to provisioned=true
        Hook-->>IoT: Return allowProvisioning=true
        IoT->>IoT: Create unique Thing, attach Policy, and activate Certificate
        IoT-->>ESP32: Return Accepted (ThingName)
        Note over ESP32: Write unique certificate, private key,<br/>and ThingName permanently to NVS
    Else validation failed
        Hook-->>IoT: Return allowProvisioning=false
        IoT-->>ESP32: Return Rejected
    End
    Note over ESP32: Disconnect claim session
    ESP32->>IoT: Reconnect using unique Device Certificate
```

### Security Model
*   **Claim Policy**: Restricts devices to connect and publish/subscribe *only* on provisioning topics (`$aws/certificates/create/*` and `$aws/provisioning-templates/*`). It prevents devices from publishing telemetry or accessing other IoT resources.
*   **Device Policy**: Dynamically restricts each provisioned device using policy variables. Devices can only connect if their `ClientId` matches their `ThingName`. They are restricted to publish to `dt/${iot:Connection.Thing.ThingName}/*` and subscribe to `cmd/${iot:Connection.Thing.ThingName}/*`.
*   **Lambda Hook**: Rejects any request if the device's MAC is missing in DynamoDB, if `allowed` is false, or if the secret mismatches (uses constant-time comparison).

---

## 3. Firmware Release & OTA Flow

The platform provides automatic, event-driven rolling OTA updates triggered when a new firmware binary is uploaded to Amazon S3.

### OTA Sequence Diagram

```mermaid
sequenceDiagram
    autonumber
    actor Developer
    participant GitHub as GitHub Actions (CI/CD)
    participant OIDC as AWS IAM (OIDC)
    participant S3 as Amazon S3
    participant Lambda as AWS Lambda (ota_trigger)
    participant IoT as AWS IoT Core
    participant Device as ESP32 Device (Rust)

    Developer->>GitHub: Push tagged commit / Merge PR to main
    GitHub->>GitHub: Build ELF & package firmware.bin
    GitHub->>OIDC: Request temporary credentials via OIDC
    OIDC-->>GitHub: Temporary AWS credentials
    GitHub->>S3: Upload firmware.bin (key: firmware_vX.Y.Z.bin)
    S3->>Lambda: Trigger s3:ObjectCreated notification
    Lambda->>S3: Generate 24-hour pre-signed GET URL
    S3-->>Lambda: Pre-signed URL
    Lambda->>IoT: Query active target devices (list_things)
    Lambda->>IoT: Create AWS IoT Job (JobDoc: pre-signed URL & version)
    IoT-->>Device: Notify via $aws/things/{thingName}/jobs/notify-next
    Device->>IoT: Request job details via $aws/things/{thingName}/jobs/$next/get
    IoT-->>Device: Return job document containing pre-signed URL & version
    Note over Device: Compare version & mark Job IN_PROGRESS
    Device->>S3: Perform HTTP GET on pre-signed URL
    S3-->>Device: Stream binary payload
    Note over Device: Write binary block-by-block to inactive OTA partition
    Device->>IoT: Mark Job SUCCEEDED / FAILED
    Device->>Device: Reboot (esp_restart)
    Note over Device: Booting into new partition (Pending Verification)
    Device->>IoT: Attempt TLS MQTT connection using unique certificate
    IoT-->>Device: Connection successful
    Note over Device: Run verification checklist & call<br/>esp_ota_mark_app_valid_cancel_rollback()
```

---

## 4. Setup & Deployment Guide

### Prerequisites
*   Terraform `>= 1.5.0`
*   AWS CLI `v2`
*   Python `3.12` (required to archive Lambda packages during Terraform runs)

### AWS Authentication Configuration
Configure your local environment with credentials for your target AWS account:
```sh
aws configure --profile esp32-ztp
export AWS_PROFILE=esp32-ztp
aws sts get-caller-identity
```

### Terraform Deployment
1.  Initialize remote backend and providers:
    ```sh
    terraform init
    ```
2.  Inspect proposed changes:
    ```sh
    terraform plan
    ```
3.  Deploy:
    ```sh
    terraform apply
    ```

### SSM Parameter Store Integration
Terraform automatically publishes the deployment outputs to the AWS Systems Manager (SSM) Parameter Store. You can read them directly using the AWS CLI or build tools, eliminating the need to parse Terraform state files manually:

```sh
# Fetch the AWS IoT ATS Endpoint
aws ssm get-parameter --name "/esp32-ztp/poc/iot_endpoint" --query "Parameter.Value" --output text

# Fetch the Provisioning Template Name
aws ssm get-parameter --name "/esp32-ztp/poc/provisioning_template_name" --query "Parameter.Value" --output text

# Fetch the Claim Certificate (SecureString - Decrypted)
aws ssm get-parameter --name "/esp32-ztp/poc/claim_certificate_pem" --with-decryption --query "Parameter.Value" --output text

# Fetch the Claim Private Key (SecureString - Decrypted)
aws ssm get-parameter --name "/esp32-ztp/poc/claim_private_key" --with-decryption --query "Parameter.Value" --output text
```

### Secure GitHub Actions OIDC Trust
This infrastructure uses AWS IAM Identity Federation via OpenID Connect (OIDC) to grant secure access to GitHub Actions runners without requiring long-lived AWS secrets to be saved in GitHub.

By default, the role `esp32-ztp-github-actions-role` configured in [iam_github_oidc.tf](file:///Users/eakin/Projects/ergousha/iot-platform-infra/iam_github_oidc.tf) trusts and accepts OIDC connections from **both** of the following repositories on their `main` branches:
1.  `ergousha/esp32-opcua-gateway-rust`
2.  `ergousha/esp32-display-vaktus-salat-rust`

To add a new repository, modify the `github_repositories` variable list in [variables.tf](file:///Users/eakin/Projects/ergousha/iot-platform-infra/variables.tf#L31-L47).

---

## 5. Project Costs & Teardown

### Cost Breakdown
All resources deployed in this repository are serverless and billed purely on-demand, costing **~$0.00 USD/month when idle**:

| Service | Billing Model | PoC Cost |
| :--- | :--- | :--- |
| **IoT Core** | per connectivity-minute & message | Negligible for PoC connections |
| **DynamoDB** | PAY_PER_REQUEST (On-Demand) | Billed per read/write; free tier covers PoC |
| **AWS Lambda** | per invocation & duration | Billed per millisecond; free tier covers PoC |
| **Amazon S3** | per GB-month & requests | Storage cost for binaries is fractions of a cent |
| **CloudWatch Logs** | per GB ingested/stored | Kept low via a 7-day retention period |

### Teardown
To destroy all Terraform-managed resources:
```sh
terraform destroy
```
