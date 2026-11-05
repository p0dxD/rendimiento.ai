#!/bin/sh
# rendimiento's installer for one computer (Linux):
#
#   curl -fsSL https://rendimiento.ai/instalar | sh
#
# It uses the container engine the computer has (containerd with nerdctl,
# recommended, or Docker), or installs containerd when there is none; puts
# the rendimiento program in ~/.local/bin; and leaves it running as a
# service of your user that starts with the computer. Then it opens
# http://localhost:8080, where the setup page connects GitHub.
#
# Settings, all optional:
#   RENDIMIENTO_GITHUB   your GitHub login (asked when not set)
#   RENDIMIENTO_VERSION  a release such as v0.1.0 (default: the latest)
#   RENDIMIENTO_SI=1     answer yes to every question (no terminal needed)
set -eu

REPO="rendimiento-ai/rendimiento"
NERDCTL_VERSION="2.4.2"

# ---- messages, in Spanish or English as the computer is set ----
case "${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}" in
es*) ES=1 ;;
*) ES= ;;
esac
say() { if [ -n "$ES" ]; then printf '%s\n' "$2"; else printf '%s\n' "$1"; fi; }
step() { if [ -n "$ES" ]; then printf '\n==> %s\n' "$2"; else printf '\n==> %s\n' "$1"; fi; }
fail() { say "rendimiento: $1" "rendimiento: $2" >&2; exit 1; }

# ask "question en" "pregunta es": yes unless answered no.
ask() {
	[ "${RENDIMIENTO_SI:-}" = 1 ] && return 0
	[ -r /dev/tty ] || fail "$1 (run with RENDIMIENTO_SI=1 to answer yes)" "$2 (córralo con RENDIMIENTO_SI=1 para contestar que sí)"
	if [ -n "$ES" ]; then printf '%s [S/n] ' "$2"; else printf '%s [Y/n] ' "$1"; fi
	read -r answer </dev/tty || answer=
	case "$answer" in
	[nN]*) return 1 ;;
	*) return 0 ;;
	esac
}

need() { command -v "$1" >/dev/null 2>&1; }

# ---- the computer ----
[ "$(uname -s)" = Linux ] || fail "this installer is for Linux for now; Windows (WSL2) and Mac come later" "este instalador es para Linux por ahora; Windows (WSL2) y Mac vienen después"
case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) fail "this processor ($(uname -m)) is not supported" "este procesador ($(uname -m)) no está soportado" ;;
esac
[ "$(id -u)" != 0 ] || fail "run it as your own user, not root: it asks for sudo when it needs it" "córralo con su propio usuario, no como root: pide sudo cuando lo necesita"
need curl || fail "curl is needed" "se necesita curl"
need systemctl || fail "systemd is needed (rendimiento runs as a service of your user)" "se necesita systemd (rendimiento corre como servicio de su usuario)"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export PATH="$HOME/.local/bin:/usr/local/bin:$PATH"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# ---- the container engine ----
step "Container engine" "Motor de contenedores"
ENGINE=
if need nerdctl && nerdctl info >/dev/null 2>&1; then
	ENGINE=nerdctl
	say "containerd (nerdctl) is ready." "containerd (nerdctl) está listo."
elif need docker && docker info >/dev/null 2>&1; then
	ENGINE=docker
	say "Docker is ready. It works; containerd is recommended (lighter, and without root)." \
		"Docker está listo. Funciona; se recomienda containerd (más ligero, y sin root)."
elif need nerdctl && need containerd-rootless-setuptool.sh; then
	ENGINE=nerdctl # installed; it is set up for this user below
	say "containerd is installed; it gets set up for your user." "containerd está instalado; se configura para su usuario."
elif need docker; then
	fail "Docker is installed, but your user cannot use it: add yourself to the docker group (sudo usermod -aG docker $USER), sign in again, and run this again" \
		"Docker está instalado, pero su usuario no puede usarlo: agréguese al grupo docker (sudo usermod -aG docker $USER), vuelva a entrar y corra esto de nuevo"
fi

if [ -z "$ENGINE" ]; then
	say "There is no container engine. rendimiento can install containerd $NERDCTL_VERSION (with nerdctl and BuildKit) for your user, without root." \
		"No hay motor de contenedores. rendimiento puede instalar containerd $NERDCTL_VERSION (con nerdctl y BuildKit) para su usuario, sin root."
	say "It needs sudo once: to put it in /usr/local, install uidmap, and, on recent Ubuntu, let rootlesskit use user namespaces." \
		"Necesita sudo una vez: para ponerlo en /usr/local, instalar uidmap y, en Ubuntu reciente, dejar que rootlesskit use espacios de nombres de usuario."
	ask "Install containerd now?" "¿Instalar containerd ahora?" || fail "install containerd (nerdctl) or Docker, then run this again" "instale containerd (nerdctl) o Docker, y corra esto de nuevo"
	need sudo || fail "sudo is needed to install containerd" "se necesita sudo para instalar containerd"
	if need apt-get; then
		sudo apt-get install -y uidmap
	elif need dnf; then
		sudo dnf install -y shadow-utils
	fi
	need newuidmap || fail "newuidmap (the uidmap package) is needed" "se necesita newuidmap (el paquete uidmap)"
	curl -fsSL -o "$tmp/nerdctl.tgz" "https://github.com/containerd/nerdctl/releases/download/v$NERDCTL_VERSION/nerdctl-full-$NERDCTL_VERSION-linux-$ARCH.tar.gz"
	sudo tar -C /usr/local -xzf "$tmp/nerdctl.tgz"
	if [ -f /proc/sys/kernel/apparmor_restrict_unprivileged_userns ] && [ "$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns)" = 1 ]; then
		sudo tee /etc/apparmor.d/usr.local.bin.rootlesskit >/dev/null <<'EOF'
abi <abi/4.0>,
include <tunables/global>
/usr/local/bin/rootlesskit flags=(unconfined) {
  userns,
  include if exists <local/usr.local.bin.rootlesskit>
}
EOF
		sudo systemctl reload apparmor
	fi
	ENGINE=nerdctl
fi

if [ "$ENGINE" = nerdctl ]; then
	# containerd and BuildKit for this user, started with the computer.
	if ! loginctl show-user "$(id -un)" 2>/dev/null | grep -q '^Linger=yes'; then
		say "sudo may ask for your password, to let your services run while you are not signed in." \
			"sudo puede pedir su contraseña, para que sus servicios corran aunque usted no haya entrado."
		sudo loginctl enable-linger "$(id -un)"
	fi
	nerdctl info >/dev/null 2>&1 || containerd-rootless-setuptool.sh install
	[ -S "$XDG_RUNTIME_DIR/buildkit/buildkitd.sock" ] || containerd-rootless-setuptool.sh install-buildkit
fi

# ---- the program ----
step "rendimiento" "rendimiento"
VERSION="${RENDIMIENTO_VERSION:-latest}"
if [ "$VERSION" = latest ]; then
	base="https://github.com/$REPO/releases/latest/download"
else
	base="https://github.com/$REPO/releases/download/$VERSION"
fi
mkdir -p "$HOME/.local/bin"
curl -fsSL -o "$tmp/rendimiento" "$base/rendimiento-linux-$ARCH" || fail "could not download rendimiento ($base)" "no se pudo descargar rendimiento ($base)"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "could not download its checksums" "no se pudieron descargar sus sumas de verificación"
want=$(grep " rendimiento-linux-$ARCH\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
got=$(sha256sum "$tmp/rendimiento" | cut -d' ' -f1)
[ -n "$want" ] && [ "$want" = "$got" ] || fail "the download does not match its checksum; nothing was installed" "la descarga no coincide con su suma de verificación; no se instaló nada"
install -m 0755 "$tmp/rendimiento" "$HOME/.local/bin/rendimiento"
say "Installed in ~/.local/bin/rendimiento." "Instalado en ~/.local/bin/rendimiento."

# ---- who signs in first ----
GITHUB="${RENDIMIENTO_GITHUB:-}"
if [ -z "$GITHUB" ]; then
	[ -r /dev/tty ] || fail "set RENDIMIENTO_GITHUB to your GitHub login" "ponga su usuario de GitHub en RENDIMIENTO_GITHUB"
	if [ -n "$ES" ]; then printf 'Su usuario de GitHub (el primer administrador): '; else printf 'Your GitHub login (the first administrator): '; fi
	read -r GITHUB </dev/tty
fi
case "$GITHUB" in
"" | *[!A-Za-z0-9-]*) fail "that is not a GitHub login" "ese no es un usuario de GitHub" ;;
esac

# ---- the service ----
step "Service" "Servicio"
unit="$HOME/.config/systemd/user/rendimiento.service"
mkdir -p "$(dirname "$unit")"
cat >"$unit" <<EOF
[Unit]
Description=rendimiento: deploy any app; AI agents fix what breaks
After=containerd.service buildkit.service
Wants=containerd.service buildkit.service

[Service]
Environment=MODE=local
Environment=ALLOWED_USERS=$GITHUB
Environment=PATH=$HOME/.local/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin
ExecStart=$HOME/.local/bin/rendimiento
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
systemctl --user daemon-reload
systemctl --user enable rendimiento.service >/dev/null
systemctl --user restart rendimiento.service

i=0
until curl -fsS -m 2 http://127.0.0.1:8080/healthz >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -lt 60 ] || fail "rendimiento did not start; see: journalctl --user -u rendimiento" "rendimiento no arrancó; vea: journalctl --user -u rendimiento"
	sleep 2
done

step "Ready" "Listo"
say "rendimiento runs at http://localhost:8080, and your apps at http://<app>.localhost:8000." \
	"rendimiento corre en http://localhost:8080, y sus aplicaciones en http://<app>.localhost:8000."
say "The setup page asks for a token; read it with:" "La página de configuración pide una ficha; léala con:"
printf '  cat %s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/rendimiento/ficha-configuracion"
say "Stop it with: systemctl --user stop rendimiento" "Deténgalo con: systemctl --user stop rendimiento"
if need xdg-open && [ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ]; then
	xdg-open http://localhost:8080 >/dev/null 2>&1 || true
fi
