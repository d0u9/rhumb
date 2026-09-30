package render_test

// This exercises the exact template text migrated into
// ~/confgen/services/shadowsocks-rust — the multi-port,
// shared case docs/inventory.md#a-shared-identity-alongside-a-principals-own
// describes, rendered against fixture data shaped like that inventory rather
// than a minimal one. It is a regression test for the migration, not a
// generic feature test — see internal/conf/render/integration_test.go for
// that.

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/render"
	"github.com/d0u9/rhumb/secretstore"
)

const ssServerTemplate = `{{- define "port" -}}
{{- $port := . -}}
{{- $userList := slice -}}
{{- range $p := principals $port.name -}}
  {{- $userList = append $userList (dict "name" $p.Name "password" $p.Secret) -}}
{{- end -}}
{{- merge (dict "server_port" $port.number "password" (secret "psk" $port.name) "users" $userList) (defaults) | toJSON -}}
{{- end -}}
{
  "servers": [
    {{- $first := true -}}
    {{- range $name, $number := (instance).ports -}}
      {{- if not $first }},{{ end -}}
      {{ template "port" (dict "name" $name "number" $number) }}
      {{- $first = false -}}
    {{- end -}}
  ]
}
`

const ssServerDefaults = `server: "::"
method: 2022-blake3-aes-256-gcm
mode: tcp_and_udp
timeout: 300
no_delay: true
keep_alive: 15
udp_timeout: 300
udp_max_associations: 4096
fast_open: true
users: []
`

const ssClientTemplate = `{{- $password := printf "%s:%s" (join ":" (upstream).shared) (upstream).secret -}}
{{- $server := merge (dict "server" (upstream).address "server_port" (upstream).port "password" $password) (defaults) -}}
{{- $ports := dict -}}
{{- if has (instance) "ports" -}}
  {{- $ports = (instance).ports -}}
{{- end -}}
{{- if eq (len $ports) 0 -}}
  {{- $ports = dict "socks" 1080 -}}
{{- end -}}
{{- $locals := slice -}}
{{- range $name, $number := $ports -}}
  {{- $proto := "socks" -}}
  {{- $mode := "tcp_and_udp" -}}
  {{- if eq $name "http" -}}
    {{- $proto = "http" -}}
    {{- $mode = "tcp_only" -}}
  {{- end -}}
  {{- $locals = append $locals (dict "local_address" "127.0.0.1" "local_port" $number "protocol" $proto "mode" $mode) -}}
{{- end -}}
{
  "servers": [{{ $server | toJSON }}],
  "locals": [
    {{- $first := true -}}
    {{- range $l := $locals -}}
      {{- if not $first }},{{ end }}{{ $l | toJSON }}
      {{- $first = false -}}
    {{- end -}}
  ]
}
`

const ssClientDefaults = `method: 2022-blake3-aes-256-gcm
mode: tcp_and_udp
timeout: 300
no_delay: true
keep_alive: 15
udp_timeout: 300
fast_open: true
`

func TestRealWorld_ShadowsocksServerCombinesOwnAcrossTwoPorts(t *testing.T) {
	inv := &inventory.Root{
		Nodes: []inventory.Node{
			{
				ID:       "u-node-group-09-01",
				Networks: inventory.Networks{"internet": "203.0.113.10"},
				Instances: []inventory.Instance{
					{ID: "ss-sea01", Service: "ssserver", Ports: inventory.Ports{
						"main":  {Number: 38250, Self: []string{"psk.main"}},
						"relay": {Number: 52146, Self: []string{"psk.relay"}},
					}},
				},
			},
			{ID: "macbook", Owner: "dana"},
		},
		Users: map[string]inventory.User{
			"dana":            {Access: []string{"sea"}},
			"erin":            {Devices: inventory.DevicesNone, Access: []string{"sea"}},
			"cn-relay":        {Devices: inventory.DevicesNone, Access: []string{"sea-relay"}},
			"default-account": {Username: "default", Devices: inventory.DevicesNone, Access: []string{"sea"}},
		},
		Routes: map[string]inventory.Route{
			"sea":       {Hops: []string{"ss-sea01:main"}},
			"sea-relay": {Hops: []string{"ss-sea01:relay"}},
		},
		Networks:  []string{"internet"},
		Universal: "internet",
	}
	manifests := map[string]confgen.Manifest{
		"ssserver": {
			Secret:   confgen.Secret{Kind: "base64", Bytes: 32},
			Auth:     confgen.AuthPerPrincipal,
			Exports:  []string{"ss-json"},
			Self:     confgen.SelfDecls{"psk": {Set: true}},
			Rotation: confgen.RotationDisruptive,
			Template: "t",
		},
		"ss-json": {Auth: confgen.AuthNone, Template: "t"},
	}

	model, err := derive.Derive(inv, manifests)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	secretsRoot := t.TempDir()
	implied := secretstore.ImpliedPaths(inv, manifests, model)
	if err := secretstore.Generate(secretsRoot, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	ownVal, err := secretstore.ReadSelf(secretsRoot, "ss-sea01")
	if err != nil {
		t.Fatal(err)
	}
	// psk is a set: one value per port, and a port hands out its own.
	psk := func(key string) string {
		values, ok := ownVal["psk"].(map[string]any)
		if !ok {
			t.Fatalf("self psk = %#v, want a map of keys", ownVal["psk"])
		}
		v, ok := values[key].(string)
		if !ok {
			t.Fatalf("self psk has no %q: %#v", key, values)
		}
		return v
	}

	renderPort := func(port string) map[string]any {
		var principals []render.Principal
		for _, p := range model.Principals("ss-sea01", port) {
			v, err := secretstore.ReadValue(secretsRoot, secretstore.Path{
				Instance: "ss-sea01", Port: port, Group: p.Group, Name: p.Slot,
			})
			if err != nil {
				t.Fatalf("ReadValue: %v", err)
			}
			principals = append(principals, render.Principal{Name: p.Name, Secret: v})
		}
		own, err := secretstore.ReadSelf(secretsRoot, "ss-sea01")
		if err != nil {
			t.Fatalf("ReadSelf: %v", err)
		}
		out, err := render.Render(render.Input{
			Target:       render.Target{Service: "ssserver", Instance: "ss-sea01"},
			Template:     ssServerTemplate,
			Defaults:     []byte(ssServerDefaults),
			DefaultsKind: confgen.DefaultsElement,
			Instance:     map[string]any{"id": "ss-sea01", "service": "ssserver", "ports": map[string]int{"main": 38250, "relay": 52146}},
			Principals:   map[string][]render.Principal{port: principals},
			Self:         own,
		})
		if err != nil {
			t.Fatalf("Render(%s): %v", port, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("Render(%s) produced invalid JSON: %v\n%s", port, err, out)
		}
		return doc
	}

	main := renderPort("main")
	servers, ok := main["servers"].([]any)
	if !ok || len(servers) != 2 {
		t.Fatalf("main servers = %#v, want 2 entries (main and relay port both rendered per port call)", main["servers"])
	}
	// This call rendered only the "main" port's principals: exactly one
	// entry carries a non-empty "users" list. Each entry carries its own
	// port's PSK, since the set has one value per port — what reaches one
	// port cannot open the other.
	seenPasswords := map[string]bool{}
	for i, s := range servers {
		entry := s.(map[string]any)
		password, ok := entry["password"].(string)
		if !ok || password == "" {
			t.Fatalf("entry %d password = %v, want its port's own psk", i, entry["password"])
		}
		if seenPasswords[password] {
			t.Fatalf("entry %d password = %v, want a different psk per port", i, password)
		}
		seenPasswords[password] = true
		if entry["method"] != "2022-blake3-aes-256-gcm" {
			t.Fatalf("entry %d missing defaults merge: %+v", i, entry)
		}
	}
	if servers[0].(map[string]any)["password"] != psk("main") {
		t.Fatalf("main entry password = %v, want the main port's psk", servers[0].(map[string]any)["password"])
	}
	usersOnMain := servers[0].(map[string]any)["users"].([]any)
	if len(usersOnMain) != 3 {
		t.Fatalf("main port users = %v, want dana-macbook, erin and default (all granted route sea)", usersOnMain)
	}
	names := map[string]bool{}
	for _, u := range usersOnMain {
		names[u.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"dana-default", "erin-default", "default-default"} {
		if !names[want] {
			t.Fatalf("main port users = %v, missing %q", usersOnMain, want)
		}
	}

	// Now the client side: dana's macbook connecting to ss-sea01:main.
	var edge *derive.Edge
	for i := range model.Edges {
		if model.Edges[i].FromInstance == "macbook-sea-ssserver-ss-json" {
			edge = &model.Edges[i]
		}
	}
	if edge == nil {
		t.Fatal("no edge for macbook-sea-ssserver-ss-json")
	}
	principalSecret, err := secretstore.ReadValue(secretsRoot, secretstore.Path{
		Instance: "ss-sea01", Port: "main", Group: "dana", Name: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	clientOut, err := render.Render(render.Input{
		Target:       render.Target{Service: "ss-json", Instance: "macbook-sea-ssserver-ss-json"},
		Template:     ssClientTemplate,
		Defaults:     []byte(ssClientDefaults),
		DefaultsKind: confgen.DefaultsElement,
		Instance:     map[string]any{"id": "macbook-sea-ssserver-ss-json", "service": "ss-json"},
		Upstream:     map[string]any{"address": edge.Address, "port": edge.Port, "secret": principalSecret, "shared": []string{psk("main")}},
	})
	if err != nil {
		t.Fatalf("Render(client): %v", err)
	}
	var clientDoc struct {
		Servers []map[string]any `json:"servers"`
		Locals  []map[string]any `json:"locals"`
	}
	if err := json.Unmarshal(clientOut, &clientDoc); err != nil {
		t.Fatalf("client output invalid JSON: %v\n%s", err, clientOut)
	}
	if len(clientDoc.Servers) != 1 {
		t.Fatalf("client servers = %+v, want 1", clientDoc.Servers)
	}
	wantPassword := psk("main") + ":" + principalSecret
	if clientDoc.Servers[0]["password"] != wantPassword {
		t.Fatalf("client password = %v, want own:principal = %q", clientDoc.Servers[0]["password"], wantPassword)
	}
	if len(clientDoc.Locals) != 1 || clientDoc.Locals[0]["local_port"] != float64(1080) {
		t.Fatalf("client locals = %+v, want one fallback socks listener on 1080 (macbook has no port override)", clientDoc.Locals)
	}

	// phone overrides ports: socks/http both named explicitly.
	phoneOut, err := render.Render(render.Input{
		Target:       render.Target{Service: "ss-json", Instance: "phone-sea-ssserver-ss-json"},
		Template:     ssClientTemplate,
		Defaults:     []byte(ssClientDefaults),
		DefaultsKind: confgen.DefaultsElement,
		Instance:     map[string]any{"id": "phone-sea-ssserver-ss-json", "ports": map[string]int{"socks": 10080, "http": 18080}},
		Upstream:     map[string]any{"address": edge.Address, "port": edge.Port, "secret": principalSecret, "shared": []string{psk("main")}},
	})
	if err != nil {
		t.Fatalf("Render(phone client): %v", err)
	}
	var phoneDoc struct {
		Locals []map[string]any `json:"locals"`
	}
	if err := json.Unmarshal(phoneOut, &phoneDoc); err != nil {
		t.Fatalf("phone output invalid JSON: %v\n%s", err, phoneOut)
	}
	if len(phoneDoc.Locals) != 2 {
		t.Fatalf("phone locals = %+v, want socks and http", phoneDoc.Locals)
	}
	byProto := map[string]map[string]any{}
	for _, l := range phoneDoc.Locals {
		byProto[l["protocol"].(string)] = l
	}
	if byProto["socks"]["local_port"] != float64(10080) || byProto["http"]["local_port"] != float64(18080) {
		t.Fatalf("phone locals = %+v, want socks:10080 http:18080", phoneDoc.Locals)
	}
	if byProto["http"]["mode"] != "tcp_only" {
		t.Fatalf("http local mode = %v, want tcp_only", byProto["http"]["mode"])
	}
}

const hy2ServerTemplate = `{{- $userpass := dict -}}
{{- range $p := principals "main" -}}
  {{- $userpass = merge (dict $p.Name $p.Secret) $userpass -}}
{{- end -}}
{{- $config := merge (dict
      "listen" (printf ":%d" (index (instance).ports "main"))
      "auth" (dict "type" "userpass" "userpass" $userpass)
    ) . -}}
{{- $config = omit $config "id" "service" "role" "ports" "bind" "values" -}}
{{ $config | toYAML }}
`

const hy2ServerDefaults = `tls:
  cert: /etc/hysteria/tls/fullchain.pem
  key: /etc/hysteria/tls/privkey.pem
ignoreClientBandwidth: false
speedTest: false
masquerade:
  type: proxy
  proxy:
    url: https://example.com/
    rewriteHost: true
`

func TestRealWorld_Hysteria2ServerListensAndAuthenticates(t *testing.T) {
	instance := map[string]any{
		"id": "hy2-sea01", "service": "hysteria2",
		"ports": map[string]int{"main": 443},
	}
	out, err := render.Render(render.Input{
		Target:       render.Target{Service: "hysteria2", Instance: "hy2-sea01"},
		Template:     hy2ServerTemplate,
		Defaults:     []byte(hy2ServerDefaults),
		DefaultsKind: confgen.DefaultsDocument,
		Instance:     instance,
		Principals: map[string][]render.Principal{
			"main": {
				{Name: "dana-default", Secret: "secret-dana"},
				{Name: "erin", Secret: "secret-erin"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output invalid YAML: %v\n%s", err, out)
	}
	if doc["listen"] != ":443" {
		t.Fatalf("listen = %v, want :443 (derived from instance.ports.main, not hardcoded in defaults)", doc["listen"])
	}
	if doc["id"] != nil || doc["ports"] != nil {
		t.Fatalf("output leaked instance bookkeeping keys: %+v", doc)
	}
	auth, ok := doc["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth = %#v, want a mapping", doc["auth"])
	}
	userpass, ok := auth["userpass"].(map[string]any)
	if !ok || userpass["dana-default"] != "secret-dana" || userpass["erin"] != "secret-erin" {
		t.Fatalf("auth.userpass = %#v", auth["userpass"])
	}
	masquerade, ok := doc["masquerade"].(map[string]any)
	if !ok {
		t.Fatalf("masquerade missing from defaults merge: %+v", doc)
	}
	proxy, ok := masquerade["proxy"].(map[string]any)
	if !ok || proxy["url"] != "https://example.com/" {
		t.Fatalf("masquerade.proxy = %#v", masquerade["proxy"])
	}
}

const microbinServerTemplate = `{{- $values := dict -}}
{{- if has (instance) "values" -}}
  {{- $values = (instance).values -}}
{{- end -}}
{{- $env := omit . "id" "service" "role" "ports" "bind" "values" -}}
{{- $env = merge (dict
      "MICROBIN_BIND" (instance).bind
      "MICROBIN_PORT" (index (instance).ports "main")
    ) $env -}}
{{- if index $values "public_path" -}}
  {{- $env = merge (dict "MICROBIN_PUBLIC_PATH" (index $values "public_path")) $env -}}
{{- end -}}
{{- if index $values "auth_enabled" -}}
  {{- $env = merge (dict
        "MICROBIN_BASIC_AUTH_USERNAME" (required (secret "auth_username") "missing MicroBin auth username")
        "MICROBIN_BASIC_AUTH_PASSWORD" (required (secret "auth_password") "missing MicroBin auth password")
        "MICROBIN_ADMIN_USERNAME" (required (secret "admin_username") "missing MicroBin admin username")
        "MICROBIN_ADMIN_PASSWORD" (required (secret "admin_password") "missing MicroBin admin password")
      ) $env -}}
{{- end -}}
{{- if index $values "upload_enabled" -}}
  {{- $env = merge (dict
        "MICROBIN_READONLY" true
        "MICROBIN_UPLOADER_PASSWORD" (required (secret "upload_password") "missing MicroBin uploader password")
      ) $env -}}
{{- end -}}
{{- range $name, $value := $env }}
{{ $name }}={{ $value | toJSON }}
{{ end -}}
`

const microbinServerDefaults = `MICROBIN_PRIVATE: true
MICROBIN_DEFAULT_PRIVACY: secret
MICROBIN_DATA_DIR: /var/lib/microbin
`

func TestRealWorld_MicroBinServerEmitsOwnSecretsWhenEnabled(t *testing.T) {
	instance := map[string]any{
		"id": "bin-sea01", "service": "microbin",
		"bind": "127.0.0.1", "ports": map[string]int{"main": 8080},
		"values": map[string]any{
			"public_path":    "https://clip.example.com/",
			"auth_enabled":   true,
			"upload_enabled": true,
		},
	}
	own := map[string]any{
		"auth_username":   "clip",
		"auth_password":   "auth-pw",
		"admin_username":  "admin",
		"admin_password":  "admin-pw",
		"upload_password": "upload-pw",
	}
	out, err := render.Render(render.Input{
		Target:       render.Target{Service: "microbin", Instance: "bin-sea01"},
		Template:     microbinServerTemplate,
		Defaults:     []byte(microbinServerDefaults),
		DefaultsKind: confgen.DefaultsDocument,
		Instance:     instance,
		Self:         own,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	env := parseEnvLines(string(out))
	if env["MICROBIN_BIND"] != `"127.0.0.1"` || env["MICROBIN_PORT"] != "8080" {
		t.Fatalf("env = %+v, want bind/port from the instance, not defaults", env)
	}
	if env["MICROBIN_PUBLIC_PATH"] != `"https://clip.example.com/"` {
		t.Fatalf("MICROBIN_PUBLIC_PATH = %v", env["MICROBIN_PUBLIC_PATH"])
	}
	if env["MICROBIN_BASIC_AUTH_PASSWORD"] != `"auth-pw"` || env["MICROBIN_ADMIN_PASSWORD"] != `"admin-pw"` {
		t.Fatalf("env = %+v, want auth/admin secrets emitted", env)
	}
	if env["MICROBIN_UPLOADER_PASSWORD"] != `"upload-pw"` || env["MICROBIN_READONLY"] != "true" {
		t.Fatalf("env = %+v, want upload secret and readonly emitted", env)
	}
	if env["MICROBIN_PRIVATE"] != "true" {
		t.Fatalf("env = %+v, want the shared default MICROBIN_PRIVATE to survive", env)
	}
}

// TestRealWorld_MicroBinServerSkipsOwnSecretsWhenDisabled is the case that
// matters most for this template: an instance with no "values" at all must
// not touch Own or panic on a missing map, matching the same real-world
// hazard already found and fixed for the Shadowsocks client above.
func TestRealWorld_MicroBinServerSkipsOwnSecretsWhenDisabled(t *testing.T) {
	instance := map[string]any{
		"id": "bin-test", "service": "microbin",
		"bind": "0.0.0.0", "ports": map[string]int{"main": 8080},
	}
	out, err := render.Render(render.Input{
		Target:       render.Target{Service: "microbin", Instance: "bin-test"},
		Template:     microbinServerTemplate,
		Defaults:     []byte(microbinServerDefaults),
		DefaultsKind: confgen.DefaultsDocument,
		Instance:     instance,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	env := parseEnvLines(string(out))
	if _, ok := env["MICROBIN_BASIC_AUTH_PASSWORD"]; ok {
		t.Fatalf("env = %+v, want no auth secrets when values is absent entirely", env)
	}
	if _, ok := env["MICROBIN_PUBLIC_PATH"]; ok {
		t.Fatalf("env = %+v, want no public path when values is absent", env)
	}
}

func parseEnvLines(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[k] = v
		}
	}
	return out
}
