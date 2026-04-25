# The four Zakros guests per docs/phase-1-plan.md §3.2 and
# architecture.md §4 VM Inventory. All guests sit on a single internal
# SDN VNet (see variables.tf — sdn_vnet / zakros_vlan_id /
# zakros_subnet). The last octet comes from `ip_offset`; OPNsense
# takes `.1` as the gateway.

locals {
  vm_guests = {
    minos = {
      vm_id        = 210
      description  = "Minos control plane — core, Mnemosyne, Hermes, Cerberus, Argus-bundled"
      cpu_cores    = 2
      memory_mb    = 8192
      disk_size_gb = 50
      ip_offset    = 10
    }
    labyrinth = {
      vm_id        = 212
      description  = "k3s single-node cluster — Daedalus and Iris pods"
      cpu_cores    = 4
      memory_mb    = 16384
      disk_size_gb = 200
      ip_offset    = 12
    }
    ariadne = {
      vm_id        = 213
      description  = "Log archive — Vector + Loki"
      cpu_cores    = 2
      memory_mb    = 4096
      disk_size_gb = 100
      ip_offset    = 13
    }
  }

  lxc_guests = {
    postgres = {
      vm_id        = 211
      description  = "Shared Postgres + pgvector — Minos, Argus, Mnemosyne, Iris, Cerberus schemas"
      cpu_cores    = 2
      memory_mb    = 4096
      disk_size_gb = 50
      ip_offset    = 11
    }
  }
}
