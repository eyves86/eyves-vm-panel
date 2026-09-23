#!/usr/bin/env bash
set -e

echo "=============================="
echo " Certbot (Snap) Auto Installer"
echo "=============================="

# 检测系统
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$ID
    VER=$VERSION_ID
else
    echo "无法识别系统版本"
    exit 1
fi

echo "检测到系统: $OS"

install_snap_debian() {
    apt update -y
    apt install -y snapd
    systemctl enable --now snapd.socket || true

    # 修复 snap 路径
    ln -sf /var/lib/snapd/snap /snap

    # 安装 certbot
    snap install --classic certbot

    # 软链
    ln -sf /snap/bin/certbot /usr/bin/certbot
}

install_snap_rhel() {
    # 启用 EPEL（部分系统需要）
    if command -v dnf >/dev/null 2>&1; then
        dnf install -y epel-release || true
        dnf install -y snapd
        systemctl enable --now snapd.socket || true
    else
        yum install -y epel-release || true
        yum install -y snapd
        systemctl enable --now snapd.socket || true
    fi

    # snap 经典路径
    ln -sf /var/lib/snapd/snap /snap

    # 安装 certbot
    snap install --classic certbot

    # 软链
    ln -sf /snap/bin/certbot /usr/bin/certbot
}

case "$OS" in
    ubuntu|debian)
        install_snap_debian
        ;;
    centos|rhel|almalinux|rocky)
        install_snap_rhel
        ;;
    fedora)
        dnf install -y snapd
        systemctl enable --now snapd.socket || true
        ln -sf /var/lib/snapd/snap /snap
        snap install --classic certbot
        ln -sf /snap/bin/certbot /usr/bin/certbot
        ;;
    *)
        echo "不支持的系统: $OS"
        exit 1
        ;;
esac

echo "=============================="
echo "安装完成！验证版本："
certbot --version || true
echo "=============================="