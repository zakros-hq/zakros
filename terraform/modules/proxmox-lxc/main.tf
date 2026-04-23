data "local_file" "ssh_public_key" {
  filename = pathexpand(var.ssh_public_key_path)
}

resource "proxmox_virtual_environment_container" "daedalus" {
  for_each = var.lxc_configurations

  node_name     = var.proxmox_node
  vm_id         = each.value.vm_id
  description   = each.value.description
  tags          = ["daedalus", each.key, "lxc"]
  unprivileged  = true
  start_on_boot = true

  # Debian 12 ships systemd 252, which needs nesting to run cleanly in
  # an unprivileged container (systemd-resolved, user slices, etc).
  features {
    nesting = true
  }

  cpu {
    cores = each.value.cpu_cores
  }

  memory {
    dedicated = each.value.memory_mb
  }

  disk {
    datastore_id = var.primary_datastore
    size         = each.value.disk_size_gb
  }

  operating_system {
    template_file_id = "${var.template_datastore}:vztmpl/${var.template_file}"
    type             = "debian"
  }

  initialization {
    hostname = each.key

    dns {
      servers = var.dns_servers
      domain  = var.domain_suffix
    }

    user_account {
      keys = [trimspace(data.local_file.ssh_public_key.content)]
    }

    ip_config {
      ipv4 {
        address = "dhcp"
      }
    }
  }

  network_interface {
    name    = "eth0"
    bridge  = var.bridge
    vlan_id = var.vlan_id
  }

  startup {
    order      = 1
    up_delay   = 10
    down_delay = 10
  }
}
