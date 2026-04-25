variable "proxmox_node" { type = string }

variable "zone_id" {
  type        = string
  description = "SDN zone id (keep ≤ 8 chars, lowercase — Proxmox enforces this)"
}

variable "bridge" {
  type        = string
  description = "Existing Proxmox bridge the SDN zone attaches to (VLAN-aware, typically no physical uplink)"
}

variable "vnet_id" {
  type        = string
  description = "SDN VNet id; also becomes the bridge name guests attach to (keep ≤ 8 chars, lowercase)"
}

variable "vlan_id" {
  type = number
}

variable "subnet" {
  type        = string
  description = "CIDR of the Zakros VNet; gateway is the first host (OPNsense LAN)"
}
