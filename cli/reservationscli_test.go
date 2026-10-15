package cli

import (
	"bytes"
	"github.com/d0u9/rhumb/inventory"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestReservationsRejectsDuplicateDHCPAssignmentsWithoutOutput(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	const hostYAML = `hosts:
  host-a:
    interfaces:
      wired:
        mac: "02:00:00:00:00:11"
        addresses:
          primary: {network: internet, ip: 203.0.113.20, dhcp_reservation: true}
          secondary: {network: internet, ip: 203.0.113.21, dhcp_reservation: true}
`
	var doc struct {
		Hosts map[string]inventory.Host `yaml:"hosts"`
	}
	if err := yaml.Unmarshal([]byte(hostYAML), &doc); err != nil {
		t.Fatal(err)
	}
	writeTestHost(t, root, "host-b", doc.Hosts["host-a"])
	var out bytes.Buffer
	err := Reservations(nil, &out, []string{"internet"}, nil, Settings{Root: root, Secrets: secrets})
	if err == nil || !strings.Contains(err.Error(), "multiple IPv4") {
		t.Fatalf("duplicate reservation: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("invalid inventory produced router output: %s", out.String())
	}
}
