variable "proxmox_node" { type = string }
variable "primary_datastore" { type = string }

variable "template_file" {
  type        = string
  description = "Template tarball filename (as seen by Proxmox storage)"
}
variable "template_datastore" {
  type        = string
  description = "Storage pool holding the LXC template tarball"
}

variable "bridge" {
  type        = string
  description = "Proxmox bridge each LXC attaches to"
}
variable "vlan_id" {
  type        = number
  description = "VLAN tag applied to each LXC NIC; null for untagged"
  default     = null
}
variable "dns_servers" { type = list(string) }
variable "domain_suffix" { type = string }

variable "admin_username" { type = string }
variable "ssh_public_key_path" { type = string }
variable "timezone" { type = string }

variable "lxc_configurations" {
  description = "Map of LXC name -> config"
  type = map(object({
    vm_id        = number
    description  = string
    cpu_cores    = number
    memory_mb    = number
    disk_size_gb = number
    ip_offset    = number
  }))
}
