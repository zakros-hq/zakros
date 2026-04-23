variable "proxmox_node" { type = string }
variable "primary_datastore" { type = string }
variable "image_datastore" { type = string }
variable "create_cloud_image" { type = bool }
variable "cloud_image_url" { type = string }

# Network — one NIC per guest on the parent bridge. Flat VLAN topology:
# guests DHCP on vmbr0 tagged with vlan_id; homelab router handles
# routing/NAT.
variable "bridge" {
  type        = string
  description = "Proxmox bridge each guest attaches to"
}
variable "vlan_id" {
  type        = number
  description = "VLAN tag applied to each guest NIC; null for untagged"
  default     = null
}
variable "domain_suffix" { type = string }

variable "admin_username" { type = string }
variable "admin_password_hash" { type = string }
variable "ssh_public_key_path" { type = string }
variable "timezone" { type = string }

variable "vm_configurations" {
  description = "Map of VM name -> config"
  type = map(object({
    vm_id        = number
    description  = string
    cpu_cores    = number
    memory_mb    = number
    disk_size_gb = number
    ip_offset    = number
  }))
}
