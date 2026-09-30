package render

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/confgen"
)

func TestRender_DocumentDefaults_InstanceWinsOverDefaults(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "hysteria2", Instance: "u-node-group-09"},
		Template:     "listen: {{ .listen }}\nlog: {{ .log }}\n",
		Defaults:     []byte("listen: :443\nlog: warn\n"),
		DefaultsKind: confgen.DefaultsDocument,
		Instance:     map[string]any{"values": map[string]any{"listen": ":8443"}},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "listen: :8443\nlog: warn\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestRender_ElementDefaults_InstanceReachesTemplateUnmerged(t *testing.T) {
	// The template does its own element-level merge, as the existing
	// Shadowsocks templates do.
	out, err := Render(Input{
		Target:       Target{Service: "shadowsocks-rust", Instance: "u-node-group-09"},
		Template:     `{{ $d := index (defaults).servers 0 }}{{ $s := merge (index .servers 0) $d }}{{ $s.port }}/{{ $s.timeout }}`,
		Defaults:     []byte("servers:\n  - timeout: 60\n    port: 8388\n"),
		DefaultsKind: confgen.DefaultsElement,
		Instance:     map[string]any{"servers": []any{map[string]any{"port": 9000}}},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "9000/60" {
		t.Fatalf("out = %q, want 9000/60", out)
	}
}

func TestRender_Secret_ReadsOwnByName(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "ssserver", Instance: "u-node-group-09"},
		Template:     `{{ secret "auth_password" }}`,
		DefaultsKind: confgen.DefaultsDocument,
		Self:         map[string]any{"auth_password": "hunter2"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "hunter2" {
		t.Fatalf("out = %q, want hunter2", out)
	}
}

func TestRender_Secret_MissingNameNamesIt(t *testing.T) {
	_, err := Render(Input{
		Target:       Target{Service: "svc", Instance: "inst"},
		Template:     `{{ secret "does-not-exist" }}`,
		DefaultsKind: confgen.DefaultsDocument,
		Self:         map[string]any{"other": "x"},
	})
	if err == nil {
		t.Fatal("Render: want error for missing secret")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error = %q, want it to name the missing key", err)
	}
	if !strings.Contains(err.Error(), "svc/inst") {
		t.Fatalf("error = %q, want it to name the target", err)
	}
}

func TestRender_NodeInstanceUpstreamAndPrincipals(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "ssserver", Instance: "ss-sea01"},
		Template:     `{{ (node).id }} {{ (instance).id }} {{ (upstream).address }}:{{ (upstream).port }} {{ range principals "main" }}{{ .Name }}={{ .Secret }} {{ end }}`,
		DefaultsKind: confgen.DefaultsDocument,
		Instance:     map[string]any{"id": "ss-sea01"},
		Node:         map[string]any{"id": "u-node-group-09-01"},
		Upstream:     map[string]any{"address": "203.0.113.1", "port": 443},
		Principals: map[string][]Principal{
			"main": {{Name: "dana-macbook", Secret: "abc"}, {Name: "yak", Secret: "xyz"}},
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "u-node-group-09-01 ss-sea01 203.0.113.1:443 dana-macbook=abc yak=xyz "
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestRender_UpstreamNilForATerminalInstance(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "microbin", Instance: "bin-sea01"},
		Template:     `{{ if upstream }}has upstream{{ else }}terminal{{ end }}`,
		DefaultsKind: confgen.DefaultsDocument,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "terminal" {
		t.Fatalf("out = %q, want terminal", out)
	}
}

func TestRender_Required_MissingNamesWhatAndWhere(t *testing.T) {
	_, err := Render(Input{
		Target:       Target{Service: "microbin", Instance: "u-node-group-09"},
		Template:     `{{ required .auth_password "microbin auth password" }}`,
		DefaultsKind: confgen.DefaultsDocument,
		Instance:     map[string]any{"other": 1},
	})
	if err == nil {
		t.Fatal("Render: want error for missing required value")
	}
	if !strings.Contains(err.Error(), "microbin auth password") {
		t.Fatalf("error = %q, want it to name what was missing", err)
	}
}

func TestRender_TargetFunction(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "hysteria2", Instance: "u-node-group-09"},
		Template:     `{{ (target).service }}/{{ (target).instance }}`,
		DefaultsKind: confgen.DefaultsDocument,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "hysteria2/u-node-group-09" {
		t.Fatalf("out = %q", out)
	}
}

func TestRender_MergeFirstArgumentWins(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "s", Instance: "i"},
		Template:     `{{ $m := merge (dict "a" 1) (dict "a" 2 "b" 3) }}{{ $m.a }}-{{ $m.b }}`,
		DefaultsKind: confgen.DefaultsDocument,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "1-3" {
		t.Fatalf("out = %q, want 1-3", out)
	}
}

func TestRender_MergeListsReplaceRatherThanCombine(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "s", Instance: "i"},
		Template:     `{{ $m := merge (dict "xs" (slice 1 2)) (dict "xs" (slice 9 8 7)) }}{{ len $m.xs }}`,
		DefaultsKind: confgen.DefaultsDocument,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "2" {
		t.Fatalf("out = %q, want 2 (the winning list, not merged)", out)
	}
}

func TestRender_OmitAndPick(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "s", Instance: "i"},
		Template:     `{{ $m := omit . "b" }}{{ has $m "a" }}-{{ has $m "b" }}`,
		DefaultsKind: confgen.DefaultsDocument,
		Instance:     map[string]any{"values": map[string]any{"a": 1, "b": 2}},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "true-false" {
		t.Fatalf("out = %q, want true-false", out)
	}
}

func TestRender_ToYAMLAndToJSON(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "s", Instance: "i"},
		Template:     `{{ toJSON (dict "a" 1) }}|{{ toYAML (dict "a" 1) }}`,
		DefaultsKind: confgen.DefaultsDocument,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "{\"a\":1}|a: 1\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestRender_BrokenDefaultsNamesTargetAndReason(t *testing.T) {
	_, err := Render(Input{
		Target:       Target{Service: "hysteria2", Instance: "broken-one"},
		Template:     `{{ . }}`,
		Defaults:     []byte("not: [valid"),
		DefaultsKind: confgen.DefaultsDocument,
	})
	if err == nil {
		t.Fatal("Render: want error for unparsable defaults")
	}
	if !strings.Contains(err.Error(), "hysteria2/broken-one") {
		t.Fatalf("error = %q, want it to name the target", err)
	}
}

func TestRender_UnknownDefaultsKindErrors(t *testing.T) {
	_, err := Render(Input{
		Target:       Target{Service: "s", Instance: "i"},
		Template:     `{{ . }}`,
		DefaultsKind: "bogus",
	})
	if err == nil {
		t.Fatal("Render: want error for unknown defaults kind")
	}
}

// TestB64_IsTheURLAlphabetWithoutPadding covers what a share URI needs.
// SIP002's ss:// userinfo is base64url without padding; standard base64
// would need percent-escaping inside a URL, and the client reading it would
// not find what it expects.
func TestB64_IsTheURLAlphabetWithoutPadding(t *testing.T) {
	// "??>" encodes to bytes that differ between the two alphabets, and to
	// a length that pads.
	out, err := Render(Input{
		Target:       Target{Service: "s", Instance: "i"},
		Template:     `{{ b64 "2022-blake3-aes-256-gcm:a?b>c" }}`,
		DefaultsKind: confgen.DefaultsDocument,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "=") {
		t.Fatalf("b64 = %q, want no padding", got)
	}
	if strings.ContainsAny(got, "+/") {
		t.Fatalf("b64 = %q, want the URL alphabet", got)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("decoding %q: %v", got, err)
	}
	if string(decoded) != "2022-blake3-aes-256-gcm:a?b>c" {
		t.Fatalf("round trip = %q", decoded)
	}
}

// TestB64_RefusesANonString covers a template calling it on a map or a
// number by mistake: the error names what it got, rather than encoding
// Go's rendering of it.
func TestB64_RefusesANonString(t *testing.T) {
	_, err := Render(Input{
		Target:       Target{Service: "s", Instance: "i"},
		Template:     `{{ b64 (dict "a" 1) }}`,
		DefaultsKind: confgen.DefaultsDocument,
	})
	if err == nil || !strings.Contains(err.Error(), "b64") {
		t.Fatalf("err = %v, want it to name b64", err)
	}
}

// TestRender_Secret_NarrowsByKeyAndField covers a set and a record: further
// arguments walk into the name, and fewer arguments than levels hand the
// template the map under it, which is what a range over a set needs.
func TestRender_Secret_NarrowsByKeyAndField(t *testing.T) {
	in := Input{
		Target:       Target{Service: "ssserver", Instance: "ss-sea01"},
		DefaultsKind: confgen.DefaultsDocument,
		Self: map[string]any{
			"psk":     map[string]any{"users": "a", "relays": "b"},
			"account": map[string]any{"main": map[string]any{"uuid": "u", "password": "p"}},
		},
	}
	for _, tc := range []struct{ template, want string }{
		{`{{ secret "psk" "relays" }}`, "b"},
		{`{{ secret "account" "main" "uuid" }}`, "u"},
		{`{{ index (secret "psk") "users" }}`, "a"},
	} {
		in.Template = tc.template
		out, err := Render(in)
		if err != nil {
			t.Fatalf("Render(%s): %v", tc.template, err)
		}
		if string(out) != tc.want {
			t.Fatalf("Render(%s) = %q, want %q", tc.template, out, tc.want)
		}
	}

	in.Template = `{{ secret "psk" "nonesuch" }}`
	if _, err := Render(in); err == nil {
		t.Fatal("Render: want an error naming the missing key")
	}
}

// TestRender_Join covers the function the multi-credential protocols need:
// a Shadowsocks 2022 password is the PSKs a port hands out and the user's
// own, joined in the order the port writes them.
func TestRender_Join(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "ss-json", Instance: "macbook-sea-ss-json"},
		Template:     `{{ join ":" (upstream).shared }}:{{ (upstream).secret }}`,
		DefaultsKind: confgen.DefaultsElement,
		Upstream:     map[string]any{"shared": []string{"server-psk", "second-psk"}, "secret": "user-psk"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "server-psk:second-psk:user-psk" {
		t.Fatalf("out = %q", out)
	}
}

// A reverse proxy renders one site block per downstream, matching on the
// name each route arrived at.
func TestRender_Downstreams(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "caddy", Instance: "proxy"},
		DefaultsKind: confgen.DefaultsDocument,
		Template:     "{{ range downstreams }}{{ .Published }} -> {{ .Address }}:{{ .Number }}\n{{ end }}",
		Downstreams: []Downstream{
			{Route: "clip", Instance: "bin", Port: "web", Published: "clip.example.com", Address: "10.0.0.11", Number: 8080},
			{Route: "vault", Instance: "vault", Port: "web", Published: "vault.example.com", Address: "10.0.0.11", Number: 8222},
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "clip.example.com -> 10.0.0.11:8080\nvault.example.com -> 10.0.0.11:8222\n"
	if string(out) != want {
		t.Fatalf("Render = %q, want %q", out, want)
	}
}

// An instance that is not a fan-out asks and gets nothing, rather than
// something belonging to another instance.
func TestRender_DownstreamsEmptyForOrdinaryInstance(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "ssserver", Instance: "ss-srv"},
		DefaultsKind: confgen.DefaultsDocument,
		Template:     "{{ len downstreams }}",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "0" {
		t.Fatalf("Render = %q, want %q", out, "0")
	}
}

// The service behind a proxy renders the same hostname the proxy matches on,
// which is what keeps Vaultwarden's DOMAIN from disagreeing with its site
// block. A port with no published name renders empty.
func TestRender_Published(t *testing.T) {
	out, err := Render(Input{
		Target:       Target{Service: "vaultwarden", Instance: "vault"},
		DefaultsKind: confgen.DefaultsDocument,
		Template:     `DOMAIN=https://{{ published "web" }}|{{ published "admin" }}`,
		Published:    map[string]string{"web": "vault.example.com"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "DOMAIN=https://vault.example.com|"
	if string(out) != want {
		t.Fatalf("Render = %q, want %q", out, want)
	}
}

func TestRender_DocumentDefaults_InstanceKeysStayOutOfTheDocument(t *testing.T) {
	// A node file writes a service's own settings under values, and nowhere
	// else: an instance's id, service, bind and ports are rhumb's own and are
	// reached through the instance function, not as document keys.
	out, err := Render(Input{
		Target:       Target{Service: "hysteria2", Instance: "u-node-group-09"},
		Template:     `{{ has . "id" }}|{{ .masquerade_url }}|{{ (instance).id }}`,
		Defaults:     []byte("masquerade_url: https://example.org/\n"),
		DefaultsKind: confgen.DefaultsDocument,
		Instance: map[string]any{
			"id":     "hy2-sea01-01",
			"values": map[string]any{"masquerade_url": "https://other.example/"},
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "false|https://other.example/|hy2-sea01-01"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}
