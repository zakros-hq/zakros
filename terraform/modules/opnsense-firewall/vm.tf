# OPNsense firewall — provisioned from scratch by opnsense-bootstrap on
# a FreeBSD 14 cloud-init VM. First boot:
#   1. FreeBSD boots
#   2. cloud-init seeds /conf/config.xml (WAN dhcp, LAN 10.100.0.1/24,
#      root SSH key, API key pre-hashed), authorized_keys, and /root/firstrun.sh
#   3. cloud-init runs /root/firstrun.sh which invokes
#      `opnsense-bootstrap.sh -r <release> -f -y` (the -f flag preserves
#      our seeded config.xml during the conversion)
#   4. Bootstrap reboots into OPNsense; the API key we seeded is already
#      active and the LAN interface is up at 10.100.0.1.

locals {
  lan_prefix = tonumber(split("/", var.lan_cidr)[1])
  lan_host   = cidrhost(var.lan_cidr, 1)

  # MAC addresses keyed off vm_id so rebuilds don't drift DHCP leases.
  # floor() forces integer — HCL's / returns a float, and %02x rejects floats.
  wan_mac = format("52:54:00:%02x:%02x:01", floor(var.vm_id / 256) % 256, var.vm_id % 256)
  lan_mac = format("52:54:00:%02x:%02x:02", floor(var.vm_id / 256) % 256, var.vm_id % 256)
}

data "local_file" "ssh_public_key" {
  filename = pathexpand(var.ssh_public_key_path)
}

# Hash the API secret with sha512-crypt ($6$), which is the format
# OPNsense stores in config.xml. Uses the local openssl binary.
# `query` is delivered as JSON on stdin — jq extracts it; the secret
# never appears on the argv list, so `ps` can't see it.
data "external" "api_secret_hash" {
  program = [
    "bash", "-c", <<-BASH
      set -euo pipefail
      secret="$(jq -r .SECRET)"
      salt="$(head -c 12 /dev/urandom | base64 | tr -d '=+/')"
      hash="$(openssl passwd -6 -salt "$salt" "$secret")"
      printf '{"hash":"%s"}\n' "$hash"
    BASH
  ]
  query = {
    SECRET = var.api_key_secret
  }
}

# FreeBSD only publishes .qcow2.xz and bpg/proxmox's download_file
# supports gz + zst but not xz. Instead of fighting that, we SSH to the
# Proxmox node and curl+unxz directly into its iso datastore path. The
# operator already has root SSH to the node (that's how the Proxmox
# cluster is administered), so no new auth surface.
resource "null_resource" "freebsd_image" {
  count = var.download_freebsd_image ? 1 : 0

  triggers = {
    url          = var.freebsd_image_url
    file_name    = local.freebsd_file_name
    datastore_id = var.image_datastore
  }

  provisioner "local-exec" {
    command = <<-EOT
      ssh -o StrictHostKeyChecking=accept-new ${var.proxmox_ssh_user}@${local.ssh_host} '
        set -euo pipefail
        dest="/var/lib/vz/template/iso/${local.freebsd_file_name}"
        if [ -s "$dest" ]; then exit 0; fi
        tmp="$(mktemp --suffix=.qcow2.xz)"
        curl -fL -o "$tmp" "${var.freebsd_image_url}"
        unxz -f "$tmp"
        mv "$${tmp%.xz}" "$dest"
      '
    EOT
  }
}

locals {
  # Proxmox's iso-storage volume parser only accepts .iso / .img extensions —
  # .qcow2 is rejected even though the file is fine internally. We land
  # the decompressed qcow2 under a .img name.
  freebsd_file_name = "freebsd-14.3-cloudinit-ufs.img"
  freebsd_file_id   = "${var.image_datastore}:iso/${local.freebsd_file_name}"
  ssh_host          = var.proxmox_ssh_host != "" ? var.proxmox_ssh_host : var.proxmox_node
}

resource "proxmox_virtual_environment_file" "user_data" {
  content_type = "snippets"
  datastore_id = var.image_datastore
  node_name    = var.proxmox_node

  source_raw {
    data = templatefile("${path.module}/templates/user-data.yaml.tmpl", {
      hostname         = var.vm_name
      fqdn             = "${var.vm_name}.${var.domain_suffix}"
      ssh_key          = trimspace(data.local_file.ssh_public_key.content)
      opnsense_release = var.opnsense_release
      config_xml = templatefile("${path.module}/templates/config.xml.tmpl", {
        hostname              = var.vm_name
        domain                = var.domain_suffix
        ssh_key               = trimspace(data.local_file.ssh_public_key.content)
        api_key_id            = var.api_key_id
        api_key_secret_hash   = data.external.api_secret_hash.result.hash
        lan_ip                = local.lan_host
        lan_prefix            = local.lan_prefix
        operator_ingress_cidr = var.operator_ingress_cidr
        dns_servers           = var.dns_servers
      })
    })
    file_name = "${var.vm_name}-user-data.yaml"
  }
}

resource "proxmox_virtual_environment_file" "network_data" {
  content_type = "snippets"
  datastore_id = var.image_datastore
  node_name    = var.proxmox_node

  source_raw {
    data = templatefile("${path.module}/templates/network-data.yaml.tmpl", {
      wan_mac     = local.wan_mac
      lan_mac     = local.lan_mac
      lan_ip      = local.lan_host
      lan_prefix  = local.lan_prefix
      dns_servers = var.dns_servers
    })
    file_name = "${var.vm_name}-network-data.yaml"
  }
}

resource "proxmox_virtual_environment_vm" "opnsense" {
  name        = var.vm_name
  vm_id       = var.vm_id
  node_name   = var.proxmox_node
  description = "Daedalus egress firewall (OPNsense ${var.opnsense_release} via opnsense-bootstrap). LAN ${local.lan_host}/${local.lan_prefix}"
  machine     = "q35"
  tags        = ["daedalus", "firewall", "opnsense"]

  agent {
    enabled = false # No qemu-guest-agent on OPNsense.
  }

  cpu {
    cores = var.cpu_cores
    type  = "x86-64-v3"
  }

  memory {
    dedicated = var.memory_mb
  }

  disk {
    datastore_id = var.primary_datastore
    file_id      = local.freebsd_file_id
    interface    = "virtio0"
    iothread     = true
    discard      = "on"
    size         = var.disk_size_gb
  }

  initialization {
    datastore_id         = var.primary_datastore
    user_data_file_id    = proxmox_virtual_environment_file.user_data.id
    network_data_file_id = proxmox_virtual_environment_file.network_data.id
  }

  # WAN — DHCP from the wider homelab.
  network_device {
    bridge      = var.wan_bridge
    mac_address = local.wan_mac
  }

  # LAN — Daedalus SDN VNet.
  network_device {
    bridge      = var.lan_bridge
    mac_address = local.lan_mac
  }

  lifecycle {
    ignore_changes = [
      # Cloud-init only runs on first boot; template tweaks shouldn't
      # force a recreate after OPNsense has replaced the filesystem.
      initialization[0].user_data_file_id,
      initialization[0].network_data_file_id,
    ]
  }

  # local.freebsd_file_id is a static path, not a resource attribute,
  # so Terraform can't infer this dependency from the reference alone.
  depends_on = [null_resource.freebsd_image]
}
