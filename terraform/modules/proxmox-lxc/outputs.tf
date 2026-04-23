output "guests" {
  description = "Per-LXC metadata consumed by the root outputs"
  value = {
    for name, cfg in var.lxc_configurations : name => {
      kind  = "lxc"
      vm_id = cfg.vm_id
      fqdn  = "${name}.${var.domain_suffix}"
    }
  }
}
