# Terraform provider configuration.
# Daedalus creates its own SDN zone + VNet inside Crete — it does not
# depend on the wider homelab's VLANs. External egress flows through a
# Crete-local OPNsense firewall provisioned from scratch via FreeBSD +
# opnsense-bootstrap. Terraform-managed firewall rules via the
# browningluke/opnsense provider land in a follow-up commit after the
# operator has validated the schema against a live OPNsense.

terraform {
  required_version = ">= 1.9"
  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "~> 0.84"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
    external = {
      source  = "hashicorp/external"
      version = "~> 2.3"
    }
    null = {
      source  = "hashicorp/null"
      version = "~> 3.2"
    }
  }
}

provider "proxmox" {
  endpoint  = var.proxmox_endpoint
  api_token = var.proxmox_api_token
  insecure  = var.proxmox_insecure

  ssh {
    agent    = true
    username = var.proxmox_ssh_user
  }
}
