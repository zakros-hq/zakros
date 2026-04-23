terraform {
  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "~> 0.84"
    }
    # external is used to sha512-crypt the API secret locally via
    # openssl before seeding it into config.xml.
    external = {
      source  = "hashicorp/external"
      version = "~> 2.3"
    }
    # null is used to SSH to the Proxmox node and curl+unxz the
    # FreeBSD cloud image into the iso datastore.
    null = {
      source  = "hashicorp/null"
      version = "~> 3.2"
    }
  }
}
