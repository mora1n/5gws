#!/usr/bin/env bash
set -euo pipefail

repo="${FIVEGWS_REPO:-mora1n/5gws}"
install_dir="${INSTALL_DIR:-/usr/local/bin}"
version=""
dry_run=0
run_install=1
install_args=()

usage() {
    cat <<'EOF'
用法：install.sh [--version VERSION] [--dry-run] [--download-only] [-- INSTALL_OPTIONS...]

下载并校验 5gws linux-amd64 release，将二进制安装到
/usr/local/bin/5gws，然后启动中文配置向导。

示例：
  wget -qO- https://raw.githubusercontent.com/mora1n/5gws/main/install.sh | sudo bash
  sudo bash install.sh --version <version>
  sudo bash install.sh -- --non-interactive --gateway-ip 203.0.113.10 \
    --internal-cidr 172.22.0.0/16 --ingress-iface eth0 --dot-domain dns.example.com
EOF
}

info() {
    printf '[INFO] %s\n' "$*" >&2
}

die() {
    printf '[ERR] %s\n' "$*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)
            [[ $# -ge 2 ]] || die "--version requires a value"
            version="$2"
            shift 2
            ;;
        --dry-run)
            dry_run=1
            shift
            ;;
        --download-only)
            run_install=0
            shift
            ;;
        --)
            shift
            install_args=("$@")
            break
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "未知参数：$1；5gws install 参数需要放在 -- 之后"
            ;;
    esac
done

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

download_stdout() {
    local url="$1"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url"
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -qO- "$url"
        return
    fi
    die "missing required command: curl or wget"
}

download_file() {
    local url="$1"
    local output="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fL -o "$output" "$url"
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -O "$output" "$url"
        return
    fi
    die "missing required command: curl or wget"
}

json_string() {
    local key="$1"
    sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p"
}

asset_urls() {
    sed -n 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\/5gws-linux-amd64\)".*/\1/p'
}

checksum_urls() {
    sed -n 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\/5gws-linux-amd64\.sha256\)".*/\1/p'
}

release_json_for_tag() {
    local tag="$1"
    download_stdout "https://api.github.com/repos/${repo}/releases/tags/${tag}"
}

resolve_release() {
    local json tag url checksum
    if [[ -z "$version" ]]; then
        json="$(download_stdout "https://api.github.com/repos/${repo}/releases/latest")"
        tag="$(printf '%s\n' "$json" | json_string tag_name | head -n1)"
        [[ -n "$tag" ]] || die "latest release has no tag_name"
        url="$(printf '%s\n' "$json" | asset_urls | head -n1)"
        [[ -n "$url" ]] || die "latest release has no 5gws linux-amd64 asset"
        checksum="$(printf '%s\n' "$json" | checksum_urls | head -n1)"
        [[ -n "$checksum" ]] || die "latest release has no checksum asset"
        printf '%s\n%s\n%s\n' "$tag" "$url" "$checksum"
        return
    fi

    local candidates=("$version")
    if [[ "$version" != v* ]]; then
        candidates+=("v${version}")
    fi

    for tag in "${candidates[@]}"; do
        info "trying release tag ${tag}"
        if json="$(release_json_for_tag "$tag" 2>/dev/null)"; then
            url="$(printf '%s\n' "$json" | asset_urls | head -n1)"
            checksum="$(printf '%s\n' "$json" | checksum_urls | head -n1)"
            [[ -n "$url" && -n "$checksum" ]] || die "release ${tag} lacks the linux-amd64 binary or checksum"
            printf '%s\n%s\n%s\n' "$tag" "$url" "$checksum"
            return
        fi
    done
    die "release not found for version ${version}"
}

case "$(uname -s)" in
    Linux) ;;
    *) die "unsupported OS: $(uname -s); only Linux is supported" ;;
esac

case "$(uname -m)" in
    x86_64|amd64) ;;
    *) die "unsupported architecture: $(uname -m); only linux-amd64 release assets are available" ;;
esac

require_cmd install
require_cmd mktemp
require_cmd sha256sum

if [[ "$dry_run" -eq 0 && "${EUID}" -ne 0 ]]; then
    die "this installer must run as root; use: wget -qO- https://raw.githubusercontent.com/${repo}/main/install.sh | sudo bash"
fi

mapfile -t resolved < <(resolve_release)
release_tag="${resolved[0]}"
asset_url="${resolved[1]}"
checksum_url="${resolved[2]}"

info "版本：${release_tag}"
info "安装位置：${install_dir}/5gws"

if [[ "$dry_run" -eq 1 ]]; then
    info "试运行：将下载并校验 ${asset_url}"
    if [[ "$run_install" -eq 1 ]]; then
        info "试运行：将执行 ${install_dir}/5gws install ${install_args[*]}"
    fi
    exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

binary="${tmp}/5gws-linux-amd64"
checksum="${tmp}/5gws-linux-amd64.sha256"
download_file "$asset_url" "$binary"
download_file "$checksum_url" "$checksum"
(cd "$tmp" && sha256sum -c "$(basename "$checksum")") || die "release checksum verification failed"

install -m 755 "$binary" "${install_dir}/5gws"
info "二进制已安装：${install_dir}/5gws"

if [[ "$run_install" -eq 0 ]]; then
    exit 0
fi

if [[ -r /dev/tty ]]; then
    info "启动 5gws 配置向导"
    "${install_dir}/5gws" install "${install_args[@]}" < /dev/tty
else
    info "未检测到终端，使用给定参数运行安装"
    "${install_dir}/5gws" install "${install_args[@]}"
fi
