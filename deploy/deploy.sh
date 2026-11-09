#!/usr/bin/env bash
# Build MailSender for the target's CPU and install/upgrade it as a systemd service.
#
#   deploy/deploy.sh root@1.2.3.4 [~/.ssh/key]
#
# First install creates the "mailsender" system user, /var/lib/mailsender (database, secret key)
# and /etc/mailsender.env (ports/TLS settings; never overwritten by later deploys).
set -euo pipefail

TARGET=${1:?usage: deploy/deploy.sh user@host [identity_file]}
SSH_OPTS=(-o ConnectTimeout=10)
if [ -n "${2:-}" ]; then
  SSH_OPTS+=(-i "$2" -o IdentitiesOnly=yes)
fi
cd "$(dirname "$0")/.."

ARCH=$(ssh "${SSH_OPTS[@]}" "$TARGET" uname -m)
case "$ARCH" in
  x86_64) GOARCH=amd64 ;;
  aarch64 | arm64) GOARCH=arm64 ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

echo "==> building console and linux/$GOARCH binary"
(cd web && { [ -d node_modules ] || npm ci; } && npm run build >/dev/null)
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -trimpath -ldflags "-s -w" -o "bin/mailsender-linux-$GOARCH" ./cmd/mailsender

echo "==> uploading to $TARGET"
scp "${SSH_OPTS[@]}" -q "bin/mailsender-linux-$GOARCH" "$TARGET:/tmp/mailsender.new"
scp "${SSH_OPTS[@]}" -q deploy/mailsender.service "$TARGET:/tmp/mailsender.service"

echo "==> installing"
ssh "${SSH_OPTS[@]}" "$TARGET" 'bash -s' <<'REMOTE'
set -e
# Minimal images may lack root CAs; without them TLS to SMTP providers and Let's Encrypt fails.
if [ ! -s /etc/ssl/certs/ca-certificates.crt ] && command -v apt-get >/dev/null; then
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q ca-certificates >/dev/null
fi
id mailsender >/dev/null 2>&1 || useradd --system --home-dir /var/lib/mailsender --shell /usr/sbin/nologin mailsender
install -d -o mailsender -g mailsender -m 700 /var/lib/mailsender
install -m 755 /tmp/mailsender.new /usr/local/bin/mailsender
install -m 644 /tmp/mailsender.service /etc/systemd/system/mailsender.service
unlink /tmp/mailsender.new
unlink /tmp/mailsender.service
if [ ! -f /etc/mailsender.env ]; then
  printf 'MS_LISTEN=:8080\nMS_SMTP_LISTEN=:2525\n' > /etc/mailsender.env
  chmod 600 /etc/mailsender.env
fi
systemctl daemon-reload
systemctl enable mailsender >/dev/null 2>&1
systemctl restart mailsender
sleep 2
systemctl is-active mailsender
journalctl -u mailsender --no-pager -n 30 -o cat
REMOTE
