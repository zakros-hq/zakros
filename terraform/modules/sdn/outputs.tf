output "bridge" {
  description = "SDN VNet id — pass this as the bridge argument to VM/LXC modules"
  value       = proxmox_sdn_vnet.zakros.id
}

output "applier_id" {
  description = "Reference callers depend on so VM/LXC creates wait for SDN apply"
  value       = proxmox_sdn_applier.zakros.id
}
