# Zakros SDN zone + VNet, internal to Crete. Pattern matches
# worklab/terraform/foundation/proxmox/main.tf.

resource "proxmox_sdn_zone_vlan" "zakros" {
  id     = var.zone_id
  bridge = var.bridge
  nodes  = [var.proxmox_node]
}

resource "proxmox_sdn_vnet" "zakros" {
  id    = var.vnet_id
  zone  = proxmox_sdn_zone_vlan.zakros.id
  tag   = var.vlan_id
  alias = "Zakros VNet (VLAN ${var.vlan_id})"
}

resource "proxmox_sdn_subnet" "zakros" {
  vnet    = proxmox_sdn_vnet.zakros.id
  cidr    = var.subnet
  gateway = cidrhost(var.subnet, 1)
  # DHCP is served by the OPNsense LAN interface (static .1 gateway), not
  # by SDN dnsmasq — the OPNsense provider configures that later.
}

# Apply SDN changes; without this the zone/vnet are defined in pending
# state and bridges never appear on the node.
resource "proxmox_sdn_applier" "zakros" {
  depends_on = [
    proxmox_sdn_zone_vlan.zakros,
    proxmox_sdn_vnet.zakros,
    proxmox_sdn_subnet.zakros,
  ]

  lifecycle {
    replace_triggered_by = [
      proxmox_sdn_zone_vlan.zakros,
      proxmox_sdn_vnet.zakros,
      proxmox_sdn_subnet.zakros,
    ]
  }
}
