#!/usr/bin/env bash
# Запустить ОДИН РАЗ на сервере от имени deploy-пользователя:
#   bash ~/simpleAI/deploy/server-setup.sh

set -e

APP_DIR="$HOME/simpleAI"

# Папка для бинарника
mkdir -p "$APP_DIR/bin"

# Первый билд
CGO_ENABLED=0 /usr/local/go/bin/go build -o "$APP_DIR/bin/app" "$APP_DIR/cmd/app"
echo "✓ binary built"

# Установить systemd unit
sudo cp "$APP_DIR/deploy/simpleai.service" /etc/systemd/system/simpleai.service
sudo systemctl daemon-reload
sudo systemctl enable simpleai
echo "✓ systemd unit installed"

# Разрешить deploy рестартовать сервис без пароля
SUDOERS_LINE="deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart simpleai, /usr/bin/systemctl is-active simpleai"
echo "$SUDOERS_LINE" | sudo tee /etc/sudoers.d/simpleai > /dev/null
sudo chmod 440 /etc/sudoers.d/simpleai
echo "✓ sudoers configured"

# Запустить сервис
sudo systemctl start simpleai
sudo systemctl status simpleai --no-pager
echo ""
echo "All done! Use 'sudo systemctl status simpleai' to check."
