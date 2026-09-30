#!/usr/bin/env python3
"""sing-box for a device profile, as a rhumb bundle runs it.

    singbox.py run                 put the configuration together and run sing-box
    singbox.py check               put it together and have sing-box check it
    singbox.py add <link|file>     add an outbound: an ss:// or hysteria2:// link,
                                   or a file of sing-box outbound JSON
    singbox.py list                every outbound, rhumb's and the added ones
    singbox.py remove <tag>        remove an added outbound
    singbox.py use <tag|auto>      pick the outbound while it runs
    singbox.py mode <rule|global|direct>
    singbox.py status              the mode and the outbound in use
    singbox.py panel               where the web panel is

The configuration is rhumb's: conf/config.json, rendered by the singbox
service from the profile's routes, one outbound per route behind the selector
"proxy", with an urltest "auto" among its choices and Rule, Global and Direct
modes. It is replaced when the bundle is. Outbounds added here are
var/extra/<tag>.json, this machine's own, never touched by rhumb; run adds
them to "proxy" and "auto" and writes var/config.json, which sing-box reads.
`add` and `remove` restart sing-box when it runs, through ./ctl.

The web panel and `use`/`mode` both talk to the Clash API the configuration
names; what is picked is kept in var/cache.db across restarts.
"""

import base64
import json
import os
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

BIN = os.path.dirname(os.path.realpath(__file__))
ROOT = os.path.dirname(BIN)
CONF = os.path.join(ROOT, "conf")
VAR = os.path.join(ROOT, "var")
EXTRA = os.path.join(VAR, "extra")
CONFIG = os.path.join(VAR, "config.json")


def fail(msg):
    print(msg, file=sys.stderr)
    sys.exit(1)


def read_json(path):
    with open(path) as f:
        return json.load(f)


def rendered():
    """rhumb's configuration."""
    path = os.path.join(CONF, "config.json")
    if not os.path.isfile(path):
        fail("no %s: the bundle is not installed here" % path)
    return read_json(path)


def ours():
    """rhumb's own outbounds: the ones "proxy" chooses among."""
    c = rendered()
    choices = next(o for o in c["outbounds"] if o.get("tag") == "proxy")["outbounds"]
    return [o for o in c["outbounds"] if o.get("tag") in choices and o.get("tag") != "auto"]


def added():
    out = []
    if not os.path.isdir(EXTRA):
        return out
    for name in sorted(os.listdir(EXTRA)):
        if name.endswith(".json"):
            out.append(read_json(os.path.join(EXTRA, name)))
    return out


def config():
    c = rendered()
    extra = added()
    tags = [o["tag"] for o in extra]
    for o in c["outbounds"]:
        if o.get("tag") == "proxy":
            o["outbounds"] = [t for t in o["outbounds"] if t != "auto"] + tags + ["auto"]
        elif o.get("tag") == "auto":
            o["outbounds"] = o["outbounds"] + tags
    c["outbounds"] += extra
    return c


def controller():
    return rendered()["experimental"]["clash_api"]["external_controller"]


def write():
    os.makedirs(VAR, exist_ok=True)
    tmp = CONFIG + ".tmp"
    with open(tmp, "w") as f:
        json.dump(config(), f, indent=2)
    os.chmod(tmp, 0o600)
    os.replace(tmp, CONFIG)
    return os.path.join(BIN, "sing-box")


def run():
    sb = write()
    os.execv(sb, [sb, "run", "-c", CONFIG, "-D", VAR])


def check():
    sb = write()
    sys.exit(subprocess.call([sb, "check", "-c", CONFIG, "-D", VAR]))


def b64(s):
    s = s.strip()
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4)).decode()


def from_ss(u):
    """ss://, SIP002 (userinfo base64 or plain for 2022 ciphers) or the
    older all-base64 form."""
    p = urllib.parse.urlsplit(u)
    if "@" not in p.netloc:
        p = urllib.parse.urlsplit("ss://" + b64(p.netloc) + ("#" + p.fragment if p.fragment else ""))
    info = urllib.parse.unquote(p.username or "")
    if p.password is not None:
        info += ":" + urllib.parse.unquote(p.password)
    if ":" not in info:
        info = b64(info)
    method, password = info.split(":", 1)
    return p.fragment, {
        "type": "shadowsocks", "server": p.hostname, "server_port": p.port,
        "method": method, "password": password,
    }


def from_hy2(u):
    p = urllib.parse.urlsplit(u)
    q = dict(urllib.parse.parse_qsl(p.query))
    auth = urllib.parse.unquote(p.username or "")
    if p.password is not None:
        auth += ":" + urllib.parse.unquote(p.password)
    host, _, ports = p.netloc.rpartition("@")[2].rpartition(":")
    host = host.strip("[]")
    ob = {"type": "hysteria2", "server": host, "password": auth}
    if "-" in ports or "," in ports:
        ob["server_ports"] = [r.replace("-", ":") for r in ports.split(",")]
    else:
        ob["server_port"] = int(ports or 443)
    tls = {"enabled": True, "server_name": q.get("sni") or q.get("peer") or host}
    if q.get("insecure") in ("1", "true"):
        tls["insecure"] = True
    ob["tls"] = tls
    if q.get("obfs") and q["obfs"] != "none":
        ob["obfs"] = {"type": q["obfs"], "password": q.get("obfs-password", "")}
    if q.get("mport"):
        ob.pop("server_port", None)
        ob["server_ports"] = [r.replace("-", ":") for r in q["mport"].split(",")]
    return urllib.parse.unquote(p.fragment), ob


def parse(arg):
    if arg.startswith("ss://"):
        return [from_ss(arg)]
    if arg.startswith(("hysteria2://", "hy2://")):
        return [from_hy2(arg)]
    if "://" in arg:
        fail("only ss://, hysteria2:// and hy2:// links are read; give other protocols as sing-box outbound JSON")
    data = read_json(arg)
    items = data.get("outbounds", [data]) if isinstance(data, dict) else data
    return [(o.pop("tag", ""), o) for o in items]


def restart():
    ctl = os.path.join(ROOT, "ctl")
    subprocess.call([ctl, "reload"])


def add(arg):
    taken = {o.get("tag") for o in rendered()["outbounds"]} | {o["tag"] for o in added()}
    os.makedirs(EXTRA, exist_ok=True)
    for tag, ob in parse(arg):
        tag = tag or "%s-%s" % (ob["type"], ob.get("server", "x"))
        base, n = tag, 2
        while tag in taken:
            tag, n = "%s-%d" % (base, n), n + 1
        ob["tag"] = tag
        taken.add(tag)
        path = os.path.join(EXTRA, tag.replace("/", "_") + ".json")
        with open(path, "w") as f:
            json.dump(ob, f, indent=2)
        os.chmod(path, 0o600)
        print("added", tag)
    restart()


def remove(tag):
    for name in os.listdir(EXTRA) if os.path.isdir(EXTRA) else []:
        path = os.path.join(EXTRA, name)
        if name.endswith(".json") and read_json(path).get("tag") == tag:
            os.remove(path)
            print("removed", tag)
            restart()
            return
    if tag in {o["tag"] for o in ours()}:
        fail("%s is rhumb's: take the route out of the profile's access instead" % tag)
    fail("no added outbound is tagged %s" % tag)


def api(method, path, body=None):
    where = controller()
    req = urllib.request.Request("http://%s%s" % (where, path), method=method,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=5) as r:
            text = r.read()
            return json.loads(text) if text else None
    except urllib.error.URLError as e:
        fail("sing-box is not answering on %s: %s; is it running?" % (where, e))


def panel():
    where = controller()
    host, _, port = where.rpartition(":")
    if host in ("", "0.0.0.0", "::", "[::]"):
        host = "127.0.0.1"
    print("web panel: http://%s:%s/ui/  (open in a browser; or: open http://%s:%s/ui/)" % (host, port, host, port))


def main(argv):
    cmd, args = (argv[0], argv[1:]) if argv else ("", [])
    if cmd == "run" and not args:
        run()
    elif cmd == "check" and not args:
        check()
    elif cmd == "add" and len(args) == 1:
        add(args[0])
    elif cmd == "remove" and len(args) == 1:
        remove(args[0])
    elif cmd == "list" and not args:
        for o in ours():
            print("%-24s %-12s rhumb" % (o["tag"], o["type"]))
        for o in added():
            print("%-24s %-12s added" % (o["tag"], o["type"]))
    elif cmd == "use" and len(args) == 1:
        api("PUT", "/proxies/proxy", {"name": args[0]})
    elif cmd == "mode" and len(args) == 1 and args[0].lower() in ("rule", "global", "direct"):
        api("PATCH", "/configs", {"mode": args[0].capitalize()})
    elif cmd == "panel" and not args:
        panel()
    elif cmd == "status" and not args:
        mode = api("GET", "/configs").get("mode")
        now = api("GET", "/proxies/proxy").get("now")
        print("mode %s, outbound %s" % (mode, now))
        panel()
    else:
        print(__doc__.strip().split("\n\n")[1], file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    main(sys.argv[1:])
