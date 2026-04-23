# Consolidated outputs. IPs aren't known at plan time (DHCP on the
# homelab VLAN), so the inventory below exposes hostnames/MACs instead;
# Ansible can resolve the hostname via DNS or MAC via ARP at run time.

output "guests" {
  description = "Map of guest name to its metadata"
  value       = merge(module.vms.guests, module.lxcs.guests)
}

output "ansible_inventory_yaml" {
  description = "Ansible-style inventory ready to write to inventory.yaml"
  value = yamlencode({
    all = {
      children = {
        daedalus = {
          hosts = {
            for name, g in merge(module.vms.guests, module.lxcs.guests) :
            name => {
              ansible_host  = g.fqdn
              ansible_user  = var.admin_username
              guest_type    = g.kind
              proxmox_vm_id = g.vm_id
            }
          }
        }
      }
    }
  })
}
