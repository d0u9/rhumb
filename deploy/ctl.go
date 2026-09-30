package deploy

// ctlDarwin is the ctl of a bundle for macOS. It registers the program as a
// LaunchAgent: it runs while its user is logged in, and launchd restarts it
// when it exits.
const ctlDarwin = `#!/bin/sh
# ctl for {{.M.Node}}/{{.M.Instance}} ({{.M.Service}}), written by rhumb deploy.
#
#   ./ctl install      register with launchd, link commands, start
#   ./ctl uninstall    stop, unregister, unlink; --purge also deletes var/
#   ./ctl start | stop | reload | status | logs
#   ./ctl link | unlink    put the bundle's commands on $PATH, or take them off
#   ./ctl run          run in the foreground, for debugging
#
# The bundle may live anywhere: every path below is found from this file.
set -eu

DIR=$(cd "$(dirname "$0")" && pwd -P)
LABEL={{.Label}}
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
DOMAIN="gui/$(id -u)"
SHIMS="$HOME/.local/bin"
EXPOSE="{{.Expose}}"
# A shim carries this line, which is how unlink and rhumb deploy gc know it
# is this bundle's.
MARK="# rhumb-bundle: $DIR"

command_line() { set -- {{.Args}}; for a; do printf '%s\n' "$a"; done; }

xml() { sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

write_plist() {
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
	[ -f "$PLIST" ] || { echo "not installed; run ./ctl install" >&2; exit 1; }
	loaded || launchctl bootstrap "$DOMAIN" "$PLIST"
}

stop() { if loaded; then launchctl bootout "$DOMAIN/$LABEL"; fi; }

status() {
	if loaded; then
		launchctl print "$DOMAIN/$LABEL" | grep -E "^$(printf '\t')(state|pid|last exit code) =" | sed 's/^[[:space:]]*//'
	elif [ -f "$PLIST" ]; then
		echo "installed, stopped"
	else
		echo "not installed"
	fi
}

link() {
	mkdir -p "$SHIMS"
	for name in $EXPOSE; do
		shim="$SHIMS/$name"
		if [ -e "$shim" ] && ! grep -qxF "$MARK" "$shim"; then
			echo "$shim exists and is not this bundle's; not replacing it" >&2
			exit 1
		fi
		printf '#!/bin/sh\n%s\nexec "%s/bin/%s" "$@"\n' "$MARK" "$DIR" "$name" >"$shim"
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
install) write_plist; link; stop; start ;;
uninstall)
	stop
	rm -f "$PLIST"
	unlink_shims
	if [ "${2:-}" = --purge ]; then rm -rf "$DIR/var"; fi
	;;
start) start ;;
stop) stop ;;
reload) stop; write_plist; start ;;
status) status ;;
logs) tail -n 100 "$DIR"/var/log/*.log ;;
link) link ;;
unlink) unlink_shims ;;
run) mkdir -p "$DIR/var"; cd "$DIR/var"; set -- {{.Args}}; exec "$@" ;;
*) sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
esac
`
