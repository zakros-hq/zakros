output "guests" {
  description = "Per-guest metadata consumed by the root outputs"
  value = {
    for name, cfg in var.vm_configurations : name => {
      kind  = "vm"
      vm_id = cfg.vm_id
      mac   = local.vm_mac[name]
      fqdn  = "${name}.${var.domain_suffix}"
      # ipv4_addresses is a list-of-lists — one inner list per NIC, each
      # holding that NIC's bound addresses. Populated via the qemu-guest-
      # agent, so it returns empty on first apply before the agent
      # reports and fills in on subsequent refreshes.
      ip = try([
        for addr in flatten(proxmox_virtual_environment_vm.zakros[name].ipv4_addresses) :
        addr if addr != "" && addr != "127.0.0.1" && !startswith(addr, "fe80")
      ][0], "")
    }
  }
}
