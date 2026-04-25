# Shared helpers for the deploy/*.sh install scripts. Source from each
# script that needs Terraform-output-driven defaults:
#
#     . "$(dirname "$0")/lib.sh"
#     : "${MINOS_HOST:=$(tf_guest_ip minos 2>/dev/null || true)}"
#     : "${MINOS_HOST:?run terraform apply so the minos guest is in state, or set MINOS_HOST manually}"
#
# Reads from the project's terraform/ dir (terraform.tfstate is committed
# in this repo, so no remote backend lookup is needed).

_tf_dir() {
  # Anchor on $0 (the calling script) so this works regardless of the
  # operator's cwd. $0 is the install script's path; lib.sh sits next to
  # it; `git -C <deploy>` finds the repo root.
  local script_dir
  script_dir="$(cd "$(dirname "$0")" 2>/dev/null && pwd)" || {
    echo "lib.sh: cannot resolve script dir from \$0=$0" >&2
    return 1
  }
  local repo
  repo="$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null)" || {
    echo "lib.sh: not inside a git repo (anchor: $script_dir)" >&2
    return 1
  }
  echo "${repo}/terraform"
}

# tf_guest_ip <name>
# Echoes the IPv4 of the named guest (from `terraform output -json guests`).
# Returns non-zero with empty stdout when the guest has no IP — e.g.,
# LXCs (Proxmox provider doesn't surface them) or VMs whose
# qemu-guest-agent hasn't reported yet. Callers fall back to a manual
# override.
tf_guest_ip() {
  local name="$1"
  command -v terraform >/dev/null || { echo "lib.sh: terraform not on PATH" >&2; return 1; }
  command -v jq        >/dev/null || { echo "lib.sh: jq not on PATH"        >&2; return 1; }
  local ip
  ip="$(terraform -chdir="$(_tf_dir)" output -json guests 2>/dev/null \
        | jq -r --arg n "$name" '.[$n].ip // ""')"
  [[ -n "$ip" ]] || return 1
  echo "$ip"
}
