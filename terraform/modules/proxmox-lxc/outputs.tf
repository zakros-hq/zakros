output "guests" {
  description = "Per-LXC metadata consumed by the root outputs"
  value = {
    for name, cfg in var.lxc_configurations : name => {
      kind  = "lxc"
      vm_id = cfg.vm_id
      fqdn  = "${name}.${var.domain_suffix}"
      mac   = try(proxmox_virtual_environment_container.zakros[name].network_interface[0].mac_address, "")
      # bpg/proxmox doesn't surface a container's runtime IPv4 the way it
      # does for VMs (LXC's ipv4 block only reports the config value —
      # "dhcp" here). Look the IP up via `ssh root@<crete> pct exec
      # <vmid> -- ip -4 addr show eth0` or match `mac` against the
      # homelab router's DHCP leases.
      ip = ""
    }
  }
}
