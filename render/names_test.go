package render

import (
	"errors"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/confgen"
)

func TestRenderNamesAndFlatnames(t *testing.T) {
	records := []Name{
		{Name: "service.example.com", Type: "CNAME", Value: "host-a.dev.example.com", Source: "service"},
		{Name: "host-a.dev.example.com", Type: "CNAME", Value: "host-a-en.dev.example.com", Source: "device"},
		{Name: "host-a-en.dev.example.com", Type: "A", Value: "10.0.0.10", Source: "interface"},
	}
	for _, tc := range []struct{ function, want string }{
		{"names", "service.example.com CNAME host-a.dev.example.com service\nhost-a.dev.example.com CNAME host-a-en.dev.example.com device\nhost-a-en.dev.example.com A 10.0.0.10 interface\n"},
		{"flatnames", "host-a-en.dev.example.com A 10.0.0.10 interface\nhost-a.dev.example.com A 10.0.0.10 device\nservice.example.com A 10.0.0.10 service\n"},
	} {
		t.Run(tc.function, func(t *testing.T) {
			out, err := Render(Input{DefaultsKind: confgen.DefaultsDocument, Names: map[string][]Name{"home": records}, Template: "{{ range " + tc.function + " \"home\" }}{{ .Name }} {{ .Type }} {{ .Value }} {{ .Source }}\n{{ end }}"})
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != tc.want {
				t.Fatalf("output = %q, want %q", out, tc.want)
			}
		})
	}
}

func TestRenderFlatnamesRejectsInvalidChain(t *testing.T) {
	for _, tc := range []struct{ name, target, diagnostic string }{
		{"missing target", "missing.example.com", "absent"},
		{"cycle", "service.example.com", "cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Render(Input{DefaultsKind: confgen.DefaultsDocument, Names: map[string][]Name{"home": {{Name: "service.example.com", Type: "CNAME", Value: tc.target, Source: "names.yaml"}}}, Template: `{{ range flatnames "home" }}{{ .Value }} {{ .Name }}{{ end }}`})
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("error = %v, want %s", err, tc.diagnostic)
			}
			if len(out) != 0 {
				t.Fatalf("partial output = %q", out)
			}
		})
	}
}

func TestRenderDNSDerivationErrorIsLazyAndProducesNoOutput(t *testing.T) {
	for _, function := range []string{"names", "flatnames"} {
		t.Run(function, func(t *testing.T) {
			out, err := Render(Input{DefaultsKind: confgen.DefaultsDocument, NamesError: errors.New("duplicate DNS name"), Names: map[string][]Name{"home": {{Name: "service.example.test", Type: "A", Value: "10.0.0.10"}}}, Template: "prefix {{ range " + function + " \"home\" }}{{.Name}}{{end}}"})
			if err == nil || !strings.Contains(err.Error(), "duplicate DNS name") || len(out) != 0 {
				t.Fatalf("render=%q, error=%v", out, err)
			}
		})
	}
	out, err := Render(Input{DefaultsKind: confgen.DefaultsDocument, NamesError: errors.New("duplicate DNS name"), Template: "ordinary configuration"})
	if err != nil || string(out) != "ordinary configuration" {
		t.Fatalf("unrelated render=%q error=%v", out, err)
	}
}
