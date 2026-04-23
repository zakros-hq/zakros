# Provider access. Credentials are loaded from TF_VAR_* environment
# variables — never from committed tfvars files.

variable "proxmox_endpoint" {
  type        = string
  description = "Proxmox VE API endpoint, e.g. https://crete.lan:8006/"
}

variable "proxmox_api_token" {
  type        = string
  description = "API token in '<user>!<token-id>=<uuid>' form"
  sensitive   = true
}

variable "proxmox_insecure" {
  type        = bool
  description = "Skip TLS verification of the Proxmox endpoint"
  default     = true
}

variable "proxmox_ssh_user" {
  type        = string
  description = "SSH user on the Proxmox host (bpg/proxmox uses SSH for some actions)"
  default     = "root"
}

variable "proxmox_ssh_host" {
  type        = string
  description = "SSH target for the Proxmox node. Empty = parse hostname out of proxmox_endpoint. Set explicitly if the endpoint uses an IP but your ~/.ssh/config prefers a name (or vice versa)."
  default     = ""
}

# Node / storage targets.

variable "proxmox_node" {
  type        = string
  description = "Proxmox node name to place all Daedalus guests on"
  default     = "crete"
}

variable "primary_datastore" {
  type        = string
  description = "Primary storage pool for VM and LXC disks"
  default     = "local-zfs"
}

variable "image_datastore" {
  type        = string
  description = "Storage pool for cloud images and snippets"
  default     = "local"
}

# Daedalus network — single internal VLAN on Crete, no physical uplink.
# Proxmox SDN creates the VNet; guests attach to that VNet directly.
# Egress flows through the OPNsense firewall VM (see modules/opnsense-firewall).

variable "wan_bridge" {
  type        = string
  description = "Proxmox bridge each guest attaches to (VLAN-aware, with physical uplink). Flat topology: guests DHCP on this bridge + VLAN tag; homelab router handles routing."
  default     = "vmbr0"
}

variable "guest_vlan_id" {
  type        = number
  description = "VLAN tag applied to every Daedalus guest NIC. Must exist on the upstream homelab switch."
  default     = 140
  validation {
    condition     = var.guest_vlan_id >= 2 && var.guest_vlan_id <= 4094
    error_message = "VLAN id must be between 2 and 4094."
  }
}

variable "internal_bridge" {
  type        = string
  description = "VLAN-aware Proxmox bridge with NO physical uplink that hosts the Daedalus SDN zone. Create manually: `auto vmbr1` + `iface vmbr1 inet manual` + `bridge-vlan-aware yes` + `bridge-vids 2-4094`"
  default     = "vmbr1"
}

variable "sdn_zone" {
  type        = string
  description = "Proxmox SDN VLAN zone name (Proxmox enforces ≤ 8 chars, lowercase)"
  default     = "daedalus"
  validation {
    condition     = length(var.sdn_zone) <= 8 && can(regex("^[a-z][a-z0-9]*$", var.sdn_zone))
    error_message = "sdn_zone must be ≤ 8 lowercase alphanumeric chars."
  }
}

variable "sdn_vnet" {
  type        = string
  description = "Proxmox SDN VNet name — becomes the bridge name guests attach to (≤ 8 chars, lowercase)"
  default     = "daedalan"
  validation {
    condition     = length(var.sdn_vnet) <= 8 && can(regex("^[a-z][a-z0-9]*$", var.sdn_vnet))
    error_message = "sdn_vnet must be ≤ 8 lowercase alphanumeric chars."
  }
}

variable "daedalus_vlan_id" {
  type        = number
  description = "VLAN tag for the Daedalus VNet (200+ recommended to avoid homelab overlap)"
  default     = 200
  validation {
    condition     = var.daedalus_vlan_id >= 2 && var.daedalus_vlan_id <= 4094
    error_message = "VLAN id must be between 2 and 4094."
  }
}

variable "daedalus_subnet" {
  type        = string
  description = "CIDR for the Daedalus VNet; OPNsense LAN gets .1"
  default     = "10.100.0.0/24"
}

variable "dns_servers" {
  type        = list(string)
  description = "Upstream DNS servers OPNsense forwards to and guests use"
  default     = ["1.1.1.1", "9.9.9.9"]
}

# Cloud-init defaults.

variable "admin_username" {
  type        = string
  description = "Admin user created inside every guest via cloud-init"
  default     = "daedalus"
}

variable "admin_password_hash" {
  type        = string
  description = "Pre-hashed admin password (mkpasswd -m sha-512). Empty disables password auth — SSH key only."
  default     = ""
  sensitive   = true
}

variable "ssh_public_key_path" {
  type        = string
  description = "Path to the operator SSH public key to inject into every guest"
  default     = "~/.ssh/id_ed25519.pub"
}

variable "timezone" {
  type        = string
  description = "Guest timezone"
  default     = "America/Chicago"
}

variable "domain_suffix" {
  type        = string
  description = "Domain suffix for guest FQDNs"
  default     = "daedalus.local"
}

# Image selection — operator usually leaves defaults. create_cloud_image
# downloads the Ubuntu Noble cloud image the first time; subsequent applies
# reuse it.

variable "create_cloud_image" {
  type        = bool
  description = "Download the Ubuntu Noble cloud image into Proxmox on apply"
  default     = true
}

variable "ubuntu_cloud_image_url" {
  type        = string
  description = "Ubuntu cloud image source URL"
  default     = "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
}

variable "debian_lxc_template" {
  type        = string
  description = "LXC template name (as Proxmox sees it) for the Postgres container. Check `pveam available | grep debian-12-standard` and `pveam list local` for the version currently downloaded on your node."
  default     = "debian-12-standard_12.12-1_amd64.tar.zst"
}

variable "debian_lxc_template_datastore" {
  type        = string
  description = "Storage pool that holds the LXC template tarball"
  default     = "local"
}

# OPNsense firewall — ISO + sizing. API credentials (only used once
# rules are enabled via opnsense_configure_rules) live with the provider.

variable "opnsense_vm_id" {
  type    = number
  default = 200
}

variable "opnsense_release" {
  type        = string
  description = "OPNsense release opnsense-bootstrap installs"
  default     = "26.1"
}

variable "opnsense_freebsd_image_url" {
  type        = string
  description = "FreeBSD 14.3 BASIC-CLOUDINIT qcow2.xz — the firewall module SSHes to the Proxmox node and unxz's it into the iso datastore (bpg/proxmox + Proxmox download-url both lack xz support). OPNsense doesn't yet support FreeBSD 15, so 14.3 is the current supported base."
  default     = "https://download.freebsd.org/releases/VM-IMAGES/14.3-RELEASE/amd64/Latest/FreeBSD-14.3-RELEASE-amd64-BASIC-CLOUDINIT-ufs.qcow2.xz"
}

variable "opnsense_operator_ingress_cidr" {
  type        = string
  description = "Source CIDR allowed to SSH/HTTPS into OPNsense on WAN. Empty = WAN inbound fully blocked (operator uses Crete as a jump host)."
  default     = ""
}

variable "opnsense_api_key" {
  type        = string
  description = "Plaintext OPNsense API key id. Seeded into config.xml at bootstrap."
  sensitive   = true
}

variable "opnsense_api_secret" {
  type        = string
  description = "Plaintext OPNsense API key secret. Hashed with openssl passwd -6 before landing in config.xml."
  sensitive   = true
}

# Terraform-managed firewall rules via browningluke/opnsense provider
# land in a follow-up commit. Bootstrap already seeds the API key pair
# into OPNsense's config.xml so the provider can authenticate the moment
# it's wired in.
