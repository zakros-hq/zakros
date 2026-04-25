output "vm_id" {
  description = "Proxmox VMID of the OPNsense firewall"
  value       = proxmox_virtual_environment_vm.opnsense.vm_id
}

output "lan_ip" {
  description = "OPNsense LAN address (gateway for Zakros guests)"
  value       = local.lan_host
}
