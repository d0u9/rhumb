package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/engine"
)

func TestMigrationProcedureNamesRenderedUninstallAndInstallPaths(t *testing.T) {
	root, _ := examplesRoot(t)
	l, err := engine.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	rep := &migrationReport{OldID: "nas", NewID: "nas"}
	rep.Procedure = buildMigrationProcedure(l, l, "nas", "nas", root, "", map[string]string{"node": "from=nas,to=nas"}, nil)
	var out bytes.Buffer
	rep.writeProcedure(&out, nil)
	got := out.String()
	for _, want := range []string{
		`(cd "$M/old/nas/samba/samba-nas" && ./uninstall.sh)`,
		`(cd "$M/new/nas/samba/samba-nas" && ./install.sh)`,
		"**Scenario: relocate this machine.**",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in procedure:\n%s", want, got)
		}
	}
	if strings.Contains(got, "no generated uninstall.sh") {
		t.Fatalf("uninstall manifest was not read:\n%s", got)
	}
	for ni := range l.Inv.Nodes {
		if l.Inv.Nodes[ni].ID == "nas" {
			l.Inv.Nodes[ni].Instances[0].Runtime = "" // host runtime: deploy/ is not rendered
		}
	}
	rep.Procedure = buildMigrationProcedure(l, l, "nas", "nas", root, "", map[string]string{"node": "from=nas,to=nas"}, nil)
	out.Reset()
	rep.writeProcedure(&out, nil)
	if !strings.Contains(out.String(), "no generated uninstall.sh") {
		t.Fatalf("host runtime was given a container uninstall script:\n%s", out.String())
	}
}

func TestMigrationComposeFactsReadsContainersAndMounts(t *testing.T) {
	containers, mounts, _, problem := migrationComposeFacts([]engine.File{{Path: "n/freshrss/rss/compose.yaml", Bytes: []byte(`services:
  rss:
    container_name: rss
    volumes:
      - data:/var/www/FreshRSS/data
      - /srv/rss/opml:/opml:ro
      - /tmp/anonymous
      - type: tmpfs
        target: /cache
      - type: bind
        source: /srv/rss/extensions
        target: /extensions
`)}})
	if problem != "" {
		t.Fatal(problem)
	}
	want := []migrationMount{{"volume", "/var/www/FreshRSS/data"}, {"bind", "/opml"}, {"bind", "/extensions"}}
	if len(containers) != 1 || containers[0] != migrationNamedContainer("rss") || fmt.Sprint(mounts) != fmt.Sprint(want) {
		t.Fatalf("containers = %v, mounts = %v", containers, mounts)
	}
}

func TestMigrationComposeFactsFindsUnnamedContainerByLabel(t *testing.T) {
	containers, _, _, problem := migrationComposeFacts([]engine.File{{Path: "compose.yaml", Bytes: []byte("services:\n  archive:\n    image: x\n")}})
	if problem != "" || len(containers) != 1 || containers[0].Ref != `"$(docker ps -aq --filter label=com.docker.compose.service=archive)"` {
		t.Fatalf("containers = %+v, problem = %q", containers, problem)
	}
}

func TestMigrationProcedureImportsOnlyWhenReplacing(t *testing.T) {
	run := migrationRuntime{Service: "freshrss", Instance: "rss", Script: true, Containers: []migrationContainer{migrationNamedContainer("rss")}, Mounts: []migrationMount{{"volume", "/data"}}}
	rep := &migrationReport{OldID: "network-4-linux-01", NewID: "network-4-linux-02"}
	rep.Procedure = migrationProcedure{Scenario: MigrationReplace, Old: []migrationRuntime{run}, New: []migrationRuntime{run}, Pair: map[string]string{"rss": "rss"}}
	var out bytes.Buffer
	rep.writeProcedure(&out, nil)
	got := out.String()
	for _, want := range []string{
		`id=$(docker ps -aq --filter 'name=^rss$'); [ -n "$id" ] && docker inspect -f '{{range .Mounts}}{{.Type}}|{{.Source}}|{{.Destination}}{{"\n"}}{{end}}' "$id" > "$M/data/rss/rss.mounts" || echo 'no container: rss'`,
		`sudo tar -C "$(awk -F'|' '$3=="/data"{print $2}' *.mounts)" -czpf data.tgz .`,
		`sudo tar -C "$(awk -F'|' '$3=="/data"{print $2}' *.new-mounts)" -xzpf data.tgz`,
		"docker start 'rss'  # installed in phase 4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in procedure:\n%s", want, got)
		}
	}
	rep.Procedure.Scenario = MigrationRelocate
	out.Reset()
	rep.writeProcedure(&out, nil)
	if strings.Contains(out.String(), "-xzpf") || !strings.Contains(out.String(), `(cd "$M/new/network-4-linux-02/freshrss/rss" && ./install.sh)`) {
		t.Fatalf("relocation imports data or does not install:\n%s", out.String())
	}
}

func TestMigrationProcedureSkipsOnDemandServices(t *testing.T) {
	containers, mounts, onDemand, problem := migrationComposeFacts([]engine.File{{Path: "compose.yaml", Bytes: []byte(`services:
  digest:
    container_name: digest
    volumes:
      - /srv/digest:/data
  archive:
    profiles: [manual]
    volumes:
      - /srv/digest:/data
      - /srv/archive:/archive
`)}})
	if problem != "" || len(containers) != 1 || containers[0].Name != "digest" {
		t.Fatalf("containers = %+v, problem = %q", containers, problem)
	}
	if fmt.Sprint(mounts) != fmt.Sprint([]migrationMount{{"bind", "/data"}}) {
		t.Fatalf("mounts = %v", mounts)
	}
	if len(onDemand) != 1 || onDemand[0].Service != "archive" || onDemand[0].Profiles != "manual" || fmt.Sprint(onDemand[0].Mounts) != "[/archive]" {
		t.Fatalf("on demand = %+v", onDemand)
	}
	run := migrationRuntime{Service: "ai-digest", Instance: "digest", Script: true, Containers: containers, Mounts: mounts, OnDemand: onDemand}
	rep := &migrationReport{OldID: "network-4", NewID: "network-8"}
	rep.Procedure = migrationProcedure{Scenario: MigrationRelocate, Old: []migrationRuntime{run}, New: []migrationRuntime{run}, Pair: map[string]string{"digest": "digest"}}
	var out bytes.Buffer
	rep.writeProcedure(&out, nil)
	got := out.String()
	if strings.Contains(got, "com.docker.compose.service=archive") {
		t.Fatalf("on-demand service was inspected:\n%s", got)
	}
	for _, want := range []string{"# archive runs only on demand (profiles: manual): no container to inspect", "on-demand service `archive` alone mounts `/archive`", "Keep the trailing `/`", "a copy is only a safeguard against a damaged disk"} {
		if !strings.Contains(got, want) {
			t.Errorf("procedure lacks %q:\n%s", want, got)
		}
	}
}
