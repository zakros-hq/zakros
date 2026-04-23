# Flat network: all Daedalus guests attach to vmbr0 with VLAN tag 130
# (the existing homelab VLAN), DHCP for addressing. Homelab router
# handles routing/NAT/DNS. No OPNsense, no SDN, no isolation.

module "vms" {
  source = "./modules/proxmox-vm"

  proxmox_node       = var.proxmox_node
  primary_datastore  = var.primary_datastore
  image_datastore    = var.image_datastore
  create_cloud_image = var.create_cloud_image
  cloud_image_url    = var.ubuntu_cloud_image_url

  bridge        = var.wan_bridge
  vlan_id       = var.guest_vlan_id
  domain_suffix = var.domain_suffix

  admin_username      = var.admin_username
  admin_password_hash = var.admin_password_hash
  ssh_public_key_path = var.ssh_public_key_path
  timezone            = var.timezone

  vm_configurations = local.vm_guests
}

module "lxcs" {
  source = "./modules/proxmox-lxc"

  proxmox_node      = var.proxmox_node
  primary_datastore = var.primary_datastore

  template_file      = var.debian_lxc_template
  template_datastore = var.debian_lxc_template_datastore

  bridge        = var.wan_bridge
  vlan_id       = var.guest_vlan_id
  dns_servers   = var.dns_servers
  domain_suffix = var.domain_suffix

  admin_username      = var.admin_username
  ssh_public_key_path = var.ssh_public_key_path
  timezone            = var.timezone

  lxc_configurations = local.lxc_guests
}
