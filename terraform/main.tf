# 1. SDN zone + VNet inside Crete. Must exist before any guest can
#    attach to the Daedalus bridge.
module "sdn" {
  source       = "./modules/sdn"
  proxmox_node = var.proxmox_node
  zone_id      = var.sdn_zone
  bridge       = var.internal_bridge
  vnet_id      = var.sdn_vnet
  vlan_id      = var.daedalus_vlan_id
  subnet       = var.daedalus_subnet
}

# 2. OPNsense firewall — FreeBSD 14 cloud-init VM that turns itself into
#    OPNsense on first boot via opnsense-bootstrap. Straddles vmbr0 (WAN)
#    and the SDN VNet (LAN). Zero-touch: operator generates an API key
#    pair locally (openssl rand), Terraform seeds both plaintext into the
#    provider and sha512-hashed into OPNsense's config.xml.
module "firewall" {
  source                = "./modules/opnsense-firewall"
  proxmox_node          = var.proxmox_node
  proxmox_ssh_user      = var.proxmox_ssh_user
  primary_datastore     = var.primary_datastore
  image_datastore       = var.image_datastore
  freebsd_image_url     = var.opnsense_freebsd_image_url
  opnsense_release      = var.opnsense_release
  vm_id                 = var.opnsense_vm_id
  wan_bridge            = var.wan_bridge
  lan_bridge            = module.sdn.bridge
  lan_cidr              = var.daedalus_subnet
  dns_servers           = var.dns_servers
  timezone              = var.timezone
  domain_suffix         = var.domain_suffix
  ssh_public_key_path   = var.ssh_public_key_path
  operator_ingress_cidr = var.opnsense_operator_ingress_cidr
  api_key_id            = var.opnsense_api_key
  api_key_secret        = var.opnsense_api_secret

  depends_on = [module.sdn]
}

# 3. Daedalus guests — all on one NIC on the SDN VNet, default route
#    points at the OPNsense LAN address.
module "vms" {
  source = "./modules/proxmox-vm"

  proxmox_node       = var.proxmox_node
  primary_datastore  = var.primary_datastore
  image_datastore    = var.image_datastore
  create_cloud_image = var.create_cloud_image
  cloud_image_url    = var.ubuntu_cloud_image_url

  bridge        = module.sdn.bridge
  subnet        = var.daedalus_subnet
  dns_servers   = var.dns_servers
  domain_suffix = var.domain_suffix

  admin_username      = var.admin_username
  admin_password_hash = var.admin_password_hash
  ssh_public_key_path = var.ssh_public_key_path
  timezone            = var.timezone

  vm_configurations = local.vm_guests

  depends_on = [module.sdn, module.firewall]
}

module "lxcs" {
  source = "./modules/proxmox-lxc"

  proxmox_node      = var.proxmox_node
  primary_datastore = var.primary_datastore

  template_file      = var.debian_lxc_template
  template_datastore = var.debian_lxc_template_datastore

  bridge        = module.sdn.bridge
  subnet        = var.daedalus_subnet
  dns_servers   = var.dns_servers
  domain_suffix = var.domain_suffix

  admin_username      = var.admin_username
  ssh_public_key_path = var.ssh_public_key_path
  timezone            = var.timezone

  lxc_configurations = local.lxc_guests

  depends_on = [module.sdn, module.firewall]
}
