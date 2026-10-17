#!/usr/bin/env bash
# Ставит nginx-прокси с логином перед экраном плана (запускать на сервере под deploy).
# Пароль задаётся отдельно и сюда не попадает:
#   sudo sh -c 'printf "taras:%s\n" "$(openssl passwd -apr1)" > /etc/nginx/plan.htpasswd'
set -euo pipefail
cd "$(dirname "$0")/.."
token=$(grep '^PLAN_WEB_TOKEN=' .env | head -1 | cut -d= -f2- | tr -d '"')
[ -n "$token" ] || { echo "PLAN_WEB_TOKEN пуст в .env" >&2; exit 1; }
tmp=$(mktemp)
sed "s|__PLAN_WEB_TOKEN__|$token|" deploy/nginx-plan.conf.tmpl > "$tmp"
sudo install -m 600 -o root -g root "$tmp" /etc/nginx/sites-available/plan
rm -f "$tmp"
sudo ln -sf /etc/nginx/sites-available/plan /etc/nginx/sites-enabled/plan
sudo nginx -t
sudo systemctl reload nginx
echo "ok: https://vpn.mcpgate.org:8443"
