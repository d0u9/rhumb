package deploy

// ctlDarwin is the ctl of a bundle for macOS. install only puts the program
// in place; start runs it under launchd for this login, and enable makes it
// a LaunchAgent that starts at every login. launchd restarts it when it
// exits.
const ctlDarwin = `#!/bin/sh
# ctl for {{.M.Node}}/{{.M.Instance}} ({{.M.Service}}), written by rhumb deploy.
#
#   ./ctl install      put the program in place and link commands; nothing starts
#   ./ctl start | stop     run under launchd until stopped or logged out
#   ./ctl enable | disable start at every login, or no longer
#   ./ctl uninstall    stop, disable, unlink; --purge also deletes var/
#   ./ctl reload | status | logs
#   ./ctl link | unlink    put the bundle's commands on $PATH, or take them off
#   ./ctl run          run in the foreground, for debugging
#
# The bundle may be unpacked anywhere. With a dir in its manifest, install
# copies it there and keeps var/; without, it stays where it is.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd -P)
DIR={{.Dir}}
LABEL={{.Label}}
# Enabled, the plist is in LaunchAgents and launchd loads it at login;
# started only, it is in var/ and nothing loads it again.
AGENT="$HOME/Library/LaunchAgents/$LABEL.plist"
SESSION="$DIR/var/$LABEL.plist"
# The program comes from its {{.Source}} source; BIN is its directory.
BIN={{.BinDir}}
# requires fails install when a program the bundle runs with is missing.
requires() {
	for p in {{.Requires}}; do
		command -v "$p" >/dev/null 2>&1 || { echo "this bundle needs $p on \$PATH; install it and run ./ctl install again" >&2; exit 1; }
	done
}
install_binary() {
	{{.InstallBinary}}
}
OWNER=$(id -un)
GROUP=$(id -gn)
self_signed() {
	{{.SelfSigned}}
}
DOMAIN="gui/$(id -u)"
SHIMS="$HOME/.local/bin"
EXPOSE="{{.Expose}}"
# A shim carries this line, which is how unlink and rhumb deploy gc know it
# is this bundle's.
MARK="# rhumb-bundle: $DIR"

command_line() { set -- {{.Args}}; for a; do printf '%s\n' "$a"; done; }

xml() { sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

copy_in() {
	[ "$HERE" != "$DIR" ] || return 0
	mkdir -p "$DIR"
	rm -rf "$DIR/bin" "$DIR/conf"
	for f in ctl manifest.yaml bin conf; do
		[ ! -e "$HERE/$f" ] || cp -R "$HERE/$f" "$DIR/$f"
	done
	mkdir -p "$DIR/bin"
	echo "installed into $DIR; $HERE may be deleted"
}

enabled() { [ -f "$AGENT" ]; }

# plist is the file launchd loads this program from.
plist() { if enabled; then echo "$AGENT"; else echo "$SESSION"; fi; }

write_plist() {
	PLIST=$1
	mkdir -p "$DIR/var/log" "$(dirname "$PLIST")"
	{
		cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>$LABEL</string>
	<key>RhumbBundle</key><string>$(printf %s "$DIR" | xml)</string>
	<key>ProgramArguments</key>
	<array>
EOF
		command_line | while IFS= read -r a; do
			printf '\t\t<string>%s</string>\n' "$(printf %s "$a" | xml)"
		done
		cat <<EOF
	</array>
	<key>WorkingDirectory</key><string>$(printf %s "$DIR/var" | xml)</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key><string>$(printf %s "$PATH" | xml)</string>
	</dict>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>StandardOutPath</key><string>$(printf %s "$DIR/var/log/out.log" | xml)</string>
	<key>StandardErrorPath</key><string>$(printf %s "$DIR/var/log/err.log" | xml)</string>
</dict>
</plist>
EOF
	} >"$PLIST.tmp"
	mv "$PLIST.tmp" "$PLIST"
}

loaded() { launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1; }

start() {
	[ -f "$DIR/ctl" ] || { echo "not installed; run ./ctl install" >&2; exit 1; }
	loaded && return 0
	write_plist "$(plist)"
	launchctl bootstrap "$DOMAIN" "$(plist)"
}

stop() { if loaded; then launchctl bootout "$DOMAIN/$LABEL"; fi; }

status() {
	if loaded; then
		launchctl print "$DOMAIN/$LABEL" | grep -E "^$(printf '\t')(state|pid|last exit code) =" | sed 's/^[[:space:]]*//'
	elif [ -f "$DIR/ctl" ]; then
		echo "installed, stopped"
	else
		echo "not installed"
	fi
	if enabled; then echo "starts at login"; else echo "does not start at login"; fi
}

link() {
	mkdir -p "$SHIMS"
	for name in $EXPOSE; do
		shim="$SHIMS/$name"
		if [ -e "$shim" ] && ! grep -qxF "$MARK" "$shim"; then
			echo "$shim exists and is not this bundle's; not replacing it" >&2
			exit 1
		fi
		printf '#!/bin/sh\n%s\nexec "%s/%s" "$@"\n' "$MARK" "$BIN" "$name" >"$shim"
		chmod 755 "$shim"
	done
}

unlink_shims() {
	for name in $EXPOSE; do
		shim="$SHIMS/$name"
		if [ -f "$shim" ] && grep -qxF "$MARK" "$shim"; then rm -f "$shim"; fi
	done
}

case "${1:-}" in
install)
	requires; copy_in; install_binary; self_signed; link
	echo "installed; ./ctl start runs it now, ./ctl enable at every login"
	;;
uninstall)
	stop
	rm -f "$AGENT" "$SESSION"
	unlink_shims
	if [ "${2:-}" = --purge ]; then rm -rf "$DIR/var"; fi
	;;
start) start ;;
stop) stop ;;
enable)
	was=$(loaded && echo 1 || true)
	stop
	write_plist "$AGENT"
	rm -f "$SESSION"
	# Enabling does not start what was stopped.
	[ -z "$was" ] || start
	echo "starts at every login; ./ctl start runs it now"
	;;
disable)
	was=$(loaded && echo 1 || true)
	stop
	rm -f "$AGENT"
	[ -z "$was" ] || start
	echo "no longer starts at login"
	;;
reload) if loaded; then stop; start; fi ;;
status) status ;;
logs) tail -n 100 "$DIR"/var/log/*.log ;;
link) link ;;
unlink) unlink_shims ;;
run) mkdir -p "$DIR/var"; cd "$DIR/var"; set -- {{.Args}}; exec "$@" ;;
*) sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
esac
`

// ctlDocker is the ctl of a containerised instance's bundle. install puts
// the files into the container's directory and starts it; the bundle itself
// is only what carried them there.
const ctlDocker = `#!/bin/sh
# ctl for {{.M.Node}}/{{.M.Instance}} ({{.M.Service}}), written by rhumb deploy.
#
#   ./ctl install      create networks and directories, install files, start
#   ./ctl up | down | status | logs
#   ./ctl uninstall    stop and remove the container; {{.C.Dir}} is kept
#
# COMPOSE overrides the compose command, docker compose by default.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd -P)
LABEL={{.Label}}
DIR={{q .C.Dir}}
COMPOSE=${COMPOSE:-docker compose}
compose() { $COMPOSE --project-directory "$DIR" -f "$DIR/compose.yaml" "$@"; }

# A container network is created with its range; one already there is
# checked, never removed: other containers may be on it.
networks() {
{{- range .M.Networks}}
	if ! docker network inspect {{q .Name}} >/dev/null 2>&1; then
		docker network create --subnet {{q .Subnet}}{{with .Gateway}} --gateway {{q .}}{{end}} {{q .Name}} >/dev/null
	elif ! docker network inspect {{q .Name}} | grep -q '"Subnet": "{{.Subnet}}"'; then
		echo "docker network {{.Name}} exists without subnet {{.Subnet}}: stop its containers, remove it, then run again" >&2
		exit 1
	fi
{{- end}}
	:
}

# place FILE TO MODE installs one file, keeping the replaced one as .bak
# when it differs.
place() {
	mkdir -p "$(dirname "$2")"
	if [ -f "$2" ] && ! cmp -s "$HERE/files/$1" "$2"; then
		cp -p -- "$2" "$2.bak"
	fi
	install -m "$3" "$HERE/files/$1" "$2"
}

install_files() {
	mkdir -p "$DIR"
{{- range .Creates}}
	mkdir -p {{q .Dir}}
{{- if .Mode}}
	chmod {{.Mode}} {{q .Dir}}
{{- end}}
{{- end}}
{{- range .Places}}
	place {{q .From}} {{q .To}} {{.Mode}}
{{- end}}
	install -m 0644 "$HERE/compose.yaml" "$DIR/compose.yaml"
}

case ${1:-} in
install)
	networks
	install_files
{{- if not .C.OneShot}}
	compose up -d --remove-orphans
{{- end}}
{{- if .Reload}}
	compose exec {{q .C.Name}} {{.Reload}}
{{- end}}
{{- with .C.Setup}}
	(cd "$DIR" && {{with $.C.Name}}CONTAINER={{q .}} {{end}}sh ./{{q .}})
{{- end}}
{{- if .C.OneShot}}
	echo "installed into $DIR; nothing there runs until started"
{{- else}}
	echo "{{.C.Name}} started from $DIR"
{{- end}}
	;;
up) compose up -d --remove-orphans ;;
down) compose down ;;
status) compose ps ;;
logs) shift; compose logs "$@" ;;
uninstall)
{{- with .C.Teardown}}
	(cd "$DIR" && sh ./{{q .}})
{{- end}}
	compose down --remove-orphans
	echo "stopped; $DIR and what the container kept in it are left in place"
	;;
*)
	sed -n '2,9p' "$0" >&2
	exit 2
	;;
esac
`

// ctlLinux is the ctl of a bundle for Linux. It registers the program as a
// systemd system service that runs as whoever owns the bundle's directory:
// it starts at boot, and systemd restarts it when it exits. Registering needs
// root, so ctl reruns itself through sudo when it is not.
const ctlLinux = `#!/bin/sh
# ctl for {{.M.Node}}/{{.M.Instance}} ({{.M.Service}}), written by rhumb deploy.
#
#   ./ctl install      copy into the install dir, register with systemd,
#                      link commands, start
#   ./ctl uninstall    stop, unregister, unlink; --purge also deletes var/
#   ./ctl start | stop | reload | status | logs
#   ./ctl link | unlink    put the bundle's commands on the owner's $PATH
#   ./ctl run          run in the foreground, for debugging
#
# The bundle may be unpacked anywhere. install copies ctl, bin/, conf/ and
# manifest.yaml into DIR, replacing what was there and keeping DIR/var, the
# program's state; the unpacked bundle may then be deleted. The program runs
# as the owner of the unpacked bundle, with DIR/var as its working directory.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd -P)
DIR={{.Dir}}
LABEL={{.Label}}
UNIT="/etc/systemd/system/$LABEL.service"
# The program comes from its {{.Source}} source; BIN is its directory.
BIN={{.BinDir}}
# requires fails install when a program the bundle runs with is missing.
requires() {
	for p in {{.Requires}}; do
		command -v "$p" >/dev/null 2>&1 || { echo "this bundle needs $p on \$PATH; install it and run ./ctl install again" >&2; exit 1; }
	done
}
install_binary() {
	{{.InstallBinary}}
}
self_signed() {
	{{.SelfSigned}}
}
OWNER=$(stat -c %U "$HERE")
GROUP=$(stat -c %G "$HERE")
SHIMS="$(getent passwd "$OWNER" | cut -d: -f6)/.local/bin"
EXPOSE="{{.Expose}}"
# A shim carries this line, which is how unlink knows it is this bundle's.
MARK="# rhumb-bundle: $DIR"

command_line() { set -- {{.Args}}; for a; do printf '%s\n' "$a"; done; }
# hook KEY ARGS... writes a unit line that runs ARGS as root, unless its
# program is missing or empty.
hook() {
	key=$1; shift
	[ $# -gt 0 ] && [ -s "$1" ] || return 0
	printf '%s=+' "$key"
	for a; do printf '%s ' "$(unit_word "$a")"; done
	echo
}
# run_stop_hook runs the stop hook now, as uninstall does after stopping.
run_stop_hook() { set -- {{.HookStop}}; if [ $# -gt 0 ] && [ -s "$1" ]; then "$@" || true; fi; }
env_files() { set -- {{.EnvFiles}}; for a; do printf '%s\n' "$a"; done; }

# systemd takes a path setting as the rest of the line, unquoted, and expands
# % in it.
unit_path() { printf %s "$1" | sed -e 's/%/%%/g'; }

# In a command line, systemd reads a quoted word with C-style escapes and
# expands %.
unit_word() { printf '"%s"' "$(printf %s "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/%/%%/g')"; }

as_root() { [ "$(id -u)" = 0 ] || exec sudo "$0" "$@"; }

# copy_in replaces DIR's copy of the bundle with this one, leaving var/.
copy_in() {
	[ "$HERE" != "$DIR" ] || return 0
	install -d -o "$OWNER" -g "$GROUP" -m 755 "$DIR"
	rm -rf "$DIR/bin" "$DIR/conf"
	for f in ctl manifest.yaml bin conf; do
		[ ! -e "$HERE/$f" ] || cp -a "$HERE/$f" "$DIR/$f"
	done
	mkdir -p "$DIR/bin"
	chown -R "$OWNER:$GROUP" "$DIR/ctl" "$DIR/manifest.yaml" "$DIR/bin" "$DIR/conf"
	echo "installed into $DIR; $HERE may be deleted"
}

write_unit() {
	install -d -o "$OWNER" -g "$GROUP" -m 700 "$DIR/var"
	{
		echo "# Written by $DIR/ctl. Do not edit: rebuild the bundle."
		echo "[Unit]"
		echo "Description={{.M.Service}} {{.M.Node}}/{{.M.Instance}}"
		echo "Wants=network-online.target"
		echo "After=network-online.target"
		echo
		echo "[Service]"
		echo "User=$OWNER"
		echo "Group=$GROUP"
		echo "WorkingDirectory=$(unit_path "$DIR/var")"
		env_files | while IFS= read -r f; do echo "EnvironmentFile=$(unit_path "$f")"; done
		printf 'ExecStart='
		command_line | while IFS= read -r a; do printf '%s ' "$(unit_word "$a")"; done
		echo
		hook ExecStartPre {{.HookStart}}
		hook ExecStopPost {{.HookStop}}
		echo "Restart=always"
		echo "RestartSec=5"
		echo "NoNewPrivileges=true"
		# The program gets these capabilities and no other; none is written
		# as an empty bounding set, which keeps none.
		echo "AmbientCapabilities={{.Capabilities}}"
		echo "CapabilityBoundingSet={{.Capabilities}}"
		# State a program keeps under XDG directories, such as Caddy's
		# certificates, stays in var/ with the rest of it.
		echo "Environment=$(unit_word "XDG_DATA_HOME=$DIR/var/share") $(unit_word "XDG_CONFIG_HOME=$DIR/var/config") $(unit_word "XDG_STATE_HOME=$DIR/var/state") $(unit_word "XDG_CACHE_HOME=$DIR/var/cache")"
		echo
		echo "[Install]"
		echo "WantedBy=multi-user.target"
	} >"$UNIT.tmp"
	mv "$UNIT.tmp" "$UNIT"
	systemctl daemon-reload
}

link() {
	[ -n "$EXPOSE" ] || return 0
	install -d -o "$OWNER" -g "$GROUP" "$SHIMS"
	for name in $EXPOSE; do
		shim="$SHIMS/$name"
		if [ -e "$shim" ] && ! grep -qxF "$MARK" "$shim"; then
			echo "$shim exists and is not this bundle's; not replacing it" >&2
			exit 1
		fi
		printf '#!/bin/sh\n%s\nexec "%s/%s" "$@"\n' "$MARK" "$BIN" "$name" >"$shim"
		chown "$OWNER:$GROUP" "$shim"
		chmod 755 "$shim"
	done
}

unlink_shims() {
	for name in $EXPOSE; do
		shim="$SHIMS/$name"
		if [ -f "$shim" ] && grep -qxF "$MARK" "$shim"; then rm -f "$shim"; fi
	done
}

case "${1:-}" in
install) requires; as_root "$@"; copy_in; install_binary; self_signed; write_unit; link; systemctl enable "$LABEL"; systemctl restart "$LABEL"; systemctl --no-pager status "$LABEL" | head -n 3 ;;
uninstall)
	as_root "$@"
	if [ -f "$UNIT" ]; then systemctl disable --now "$LABEL"; fi
	# Stopping ran it already; again, for a unit that was not running.
	run_stop_hook
	rm -f "$UNIT"
	systemctl daemon-reload
	unlink_shims
	if [ "${2:-}" = --purge ]; then rm -rf "$DIR/var"; fi
	;;
start) as_root "$@"; systemctl start "$LABEL" ;;
stop) as_root "$@"; systemctl stop "$LABEL" ;;
reload) as_root "$@"; write_unit; systemctl restart "$LABEL" ;;
status) systemctl --no-pager status "$LABEL" ;;
logs) shift; journalctl --no-pager -u "$LABEL" -n 100 "$@" ;;
link) as_root "$@"; link ;;
unlink) as_root "$@"; unlink_shims ;;
run)
	mkdir -p "$DIR/var"; cd "$DIR/var"
	set -a
	IFS='
'
	for f in $(env_files); do . "$f"; done
	unset IFS
	set +a
	set -- {{.Args}}; exec "$@"
	;;
*) sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
esac
`
