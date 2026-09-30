package cli

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/engine"

	"gopkg.in/yaml.v3"

	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/target"
)

// The two kinds of move a node migration is. The inventory cannot tell them
// apart, so the operator names one when the migration starts.
const (
	// MigrationRelocate moves the same machine somewhere else: its disks,
	// Docker volumes and bind paths come along.
	MigrationRelocate = "relocate"
	// MigrationReplace moves the node onto another machine, which starts
	// empty: the data travels as an archive.
	MigrationReplace = "replace"
)

var MigrationScenarios = []struct{ ID, Label, Detail string }{
	{MigrationRelocate, "Relocate this machine", "Same hardware at a new place; data stays on its disks"},
	{MigrationReplace, "Replace with a new machine", "Old hardware retires; data is archived and imported"},
}

func parseMigrationScenario(value string) (string, error) {
	switch value {
	case MigrationRelocate, MigrationReplace:
		return value, nil
	case "":
		return MigrationRelocate, nil
	}
	return "", fmt.Errorf("migration scenario %q: want %s or %s", value, MigrationRelocate, MigrationReplace)
}

// migrationProcedure is what the report's operator steps need beyond the
// change analysis: each runtime instance on the moved node, before and after,
// with the containers and mounts its rendered compose.yaml declares.
type migrationProcedure struct {
	Scenario             string
	OldBundle, NewBundle string
	OldAddresses         []string
	NewAddresses         []string
	Old, New             []migrationRuntime
	// Pair maps an old runtime instance ID to its new one.
	Pair  map[string]string
	Probe []migrationProbe
}

// migrationRuntime is one authored service instance the moved node runs.
type migrationRuntime struct {
	Service, Instance string
	Job               bool // no ports: a consumer, stopped first and started last
	Script            bool // the side's install.sh or uninstall.sh is rendered
	Containers        []migrationContainer
	Mounts            []migrationMount
	// OnDemand lists the compose services that only run when asked for,
	// under a profile: they have no container to inspect or stop.
	OnDemand []migrationOnDemand
	// Problem says why containers and mounts are unknown.
	Problem string
}

type migrationMount struct{ Type, Target string }

// migrationOnDemand is a compose service under a profile. Mounts are the
// targets it declares that no always-running service of the instance also
// mounts, so no .mounts record will locate them.
type migrationOnDemand struct {
	Service, Profiles string
	Mounts            []string
}

// migrationContainer names one container for files (Name), for commands
// (Ref, already shell-quoted) and for docker ps (Filter). A compose service
// without container_name gets a generated name, so it is found by label.
type migrationContainer struct{ Name, Ref, Filter string }

func migrationNamedContainer(name string) migrationContainer {
	return migrationContainer{Name: name, Ref: shellQuote(name), Filter: "name=^" + name + "$"}
}

// migrationProbe is one address a client dials after the move.
type migrationProbe struct {
	Route, Address, Network, Instance string
	Port                              int
}

func buildMigrationProcedure(before, after engine.Loaded, oldID, newID, root, secrets string, flags map[string]string, renames map[string]string) migrationProcedure {
	scenario, _ := parseMigrationScenario(flags["scenario"])
	p := migrationProcedure{
		Scenario:     scenario,
		OldBundle:    "migration-old-" + oldID,
		NewBundle:    "migration-new-" + newID,
		OldAddresses: migrationNodeAddresses(before, oldID),
		NewAddresses: migrationNodeAddresses(after, newID),
		Pair:         map[string]string{},
	}
	p.Old = migrationRuntimes(before, oldID, root, secrets, "uninstall.sh")
	p.New = migrationRuntimes(after, newID, root, secrets, "install.sh")
	for _, old := range p.Old {
		next := old.Instance
		if renamed := renames[old.Instance]; renamed != "" {
			next = renamed
		}
		p.Pair[old.Instance] = next
	}
	owned := map[string]bool{}
	for _, run := range p.New {
		owned[run.Instance] = true
	}
	seen := map[string]bool{}
	for _, edge := range after.Derived.Edges {
		if !owned[edge.To.Instance] {
			continue
		}
		probe := migrationProbe{Route: edge.Route, Address: edge.Address, Network: edge.Network, Instance: edge.To.Instance, Port: edge.Port}
		key := fmt.Sprintf("%s|%s|%d", probe.Address, probe.Network, probe.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		p.Probe = append(p.Probe, probe)
	}
	sort.Slice(p.Probe, func(i, j int) bool {
		a, b := p.Probe[i], p.Probe[j]
		if a.Network != b.Network {
			return a.Network < b.Network
		}
		if a.Address != b.Address {
			return a.Address < b.Address
		}
		return a.Port < b.Port
	})
	return p
}

func migrationNodeAddresses(l engine.Loaded, nodeID string) []string {
	for _, node := range l.Inv.Nodes {
		if node.ID != nodeID {
			continue
		}
		var out []string
		for _, name := range sortedKeys(node.Networks) {
			out = append(out, name+" "+node.Networks[name])
		}
		return out
	}
	return nil
}

// migrationRuntimes lists runtime instances in stop order for uninstall.sh
// (jobs first) and start order for install.sh (jobs last): a job commonly
// reads what a listening service holds.
func migrationRuntimes(l engine.Loaded, nodeID, root, secrets, script string) []migrationRuntime {
	r := engine.Renderer{Data: l, RootPath: root, SecretsDir: secrets}
	var out []migrationRuntime
	for _, item := range target.List(l.Inv, l.Derived) {
		if item.Node != nodeID || item.Service == "" || item.Export != "" || item.Broken != "" {
			continue
		}
		run := migrationRuntime{
			Service:  item.Service,
			Instance: item.Instance,
			Job:      migrationListenerless(l, nodeID, item.Instance),
			Script:   migrationHasInstanceDeployScript(l, item, script),
		}
		switch {
		case !run.Script:
			run.Problem = "no generated " + script + "; the instance is not containerised or its service has no deployment"
		case secrets == "":
			run.Problem = "no secrets root given, so compose.yaml was not rendered"
		default:
			files, err := r.RenderAll([]string{item.Instance})
			if err != nil {
				run.Problem = "render failed: " + err.Error()
				break
			}
			run.Containers, run.Mounts, run.OnDemand, run.Problem = migrationComposeFacts(files)
		}
		out = append(out, run)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Job != out[j].Job {
			return out[i].Job == (script == "uninstall.sh")
		}
		return out[i].Instance < out[j].Instance
	})
	return out
}

// migrationComposeFacts reads container names and volume targets from the
// rendered compose.yaml. A mount's host source is not read here: a named
// volume's lives where Docker puts it, so the commands ask Docker.
// migrationComposeFacts reads the containers and mounts of one rendered
// compose.yaml. A service under a profile is on demand: it has no container
// until someone runs it, so it is listed apart instead of being inspected.
func migrationComposeFacts(files []engine.File) ([]migrationContainer, []migrationMount, []migrationOnDemand, string) {
	for _, file := range files {
		if filepath.Base(file.Path) != "compose.yaml" {
			continue
		}
		var doc struct {
			Services map[string]struct {
				ContainerName string      `yaml:"container_name"`
				Profiles      []string    `yaml:"profiles"`
				Volumes       []yaml.Node `yaml:"volumes"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(file.Bytes, &doc); err != nil {
			return nil, nil, nil, "compose.yaml does not parse: " + err.Error()
		}
		var containers []migrationContainer
		var mounts []migrationMount
		var onDemand []migrationOnDemand
		mounted := map[string]bool{}
		for _, name := range sortedKeys(doc.Services) {
			service := doc.Services[name]
			if len(service.Profiles) != 0 {
				continue
			}
			for _, volume := range service.Volumes {
				if mount, ok := migrationComposeMount(volume); ok && !mounted[mount.Target] {
					mounted[mount.Target] = true
					mounts = append(mounts, mount)
				}
			}
		}
		for _, name := range sortedKeys(doc.Services) {
			service := doc.Services[name]
			if len(service.Profiles) != 0 {
				entry := migrationOnDemand{Service: name, Profiles: strings.Join(service.Profiles, ", ")}
				for _, volume := range service.Volumes {
					if mount, ok := migrationComposeMount(volume); ok && !mounted[mount.Target] {
						entry.Mounts = append(entry.Mounts, mount.Target)
					}
				}
				onDemand = append(onDemand, entry)
				continue
			}
			if service.ContainerName == "" {
				filter := "label=com.docker.compose.service=" + name
				containers = append(containers, migrationContainer{Name: name, Ref: `"$(docker ps -aq --filter ` + filter + `)"`, Filter: filter})
			} else {
				containers = append(containers, migrationNamedContainer(service.ContainerName))
			}
		}
		return containers, mounts, onDemand, ""
	}
	return nil, nil, nil, "no compose.yaml rendered"
}

func migrationComposeMount(node yaml.Node) (migrationMount, bool) {
	if node.Kind == yaml.ScalarNode {
		parts := strings.Split(node.Value, ":")
		if len(parts) < 2 {
			return migrationMount{}, false // anonymous volume: nothing to carry over
		}
		kind := "volume"
		if strings.HasPrefix(parts[0], "/") || strings.HasPrefix(parts[0], ".") || strings.HasPrefix(parts[0], "~") {
			kind = "bind"
		}
		return migrationMount{Type: kind, Target: parts[1]}, true
	}
	var long struct{ Type, Source, Target string }
	if err := node.Decode(&long); err != nil || long.Target == "" || long.Source == "" {
		return migrationMount{}, false
	}
	if long.Type == "" {
		long.Type = "volume"
	}
	if long.Type != "volume" && long.Type != "bind" {
		return migrationMount{}, false // tmpfs and the like hold nothing to keep
	}
	return migrationMount{Type: long.Type, Target: long.Target}, true
}

// The mount record is split on "|": a bind mount has no volume name, which
// would shift whitespace-separated fields.
const migrationMountsFormat = `'{{range .Mounts}}{{.Type}}|{{.Source}}|{{.Destination}}{{"\n"}}{{end}}'`

func migrationArchiveName(target string) string {
	name := strings.Trim(strings.ReplaceAll(target, "/", "_"), "_")
	if name == "" {
		name = "root"
	}
	return name + ".tgz"
}

func (r *migrationReport) writeProcedure(b *bytes.Buffer, attention []migrationAttentionItem) {
	p := r.Procedure
	replace := p.Scenario == MigrationReplace
	label := func(run migrationRuntime) string {
		return mdCode(run.Service + "/" + inventory.LocalName(run.Instance))
	}
	oldDir := func(run migrationRuntime) string {
		return "$M/old/" + r.OldID + "/" + run.Service + "/" + inventory.LocalName(run.Instance)
	}
	newDir := func(run migrationRuntime) string {
		return "$M/new/" + r.NewID + "/" + run.Service + "/" + inventory.LocalName(run.Instance)
	}

	if replace {
		b.WriteString("\n**Scenario: replace with a new machine.** The new host starts empty; data travels as an archive and phase 4 imports it.\n")
		b.WriteString("\nCommands run on the **workstation** holding the generator root, the **old host** and the **new host**.")
	} else {
		b.WriteString("\n**Scenario: relocate this machine.** The same hardware moves; Docker volumes and bind paths stay on its disks, so phase 4 has nothing to import. The archive from phase 2 is the rollback.\n")
		b.WriteString("\nCommands run on the **workstation** holding the generator root and on the **host**, before and after the move.")
	}
	b.WriteString(" `$M` is the working directory on the hosts:\n\n```sh\nM=~/rhumb-migration\n```\n")
	fmt.Fprintf(b, "\nInventory addresses — before: %s; after: %s.\n", migrationInline(p.OldAddresses), migrationInline(p.NewAddresses))

	// Phase 1.
	b.WriteString("\n### Phase 1 — Take services offline (old host)\n\nOn the workstation, check the inventory and export the bundle the old host runs from:\n\n```sh\n")
	fmt.Fprintf(b, "rhumb check\nrhumb export node:%s --to %s --yes\nssh OLD_HOST 'mkdir -p ~/rhumb-migration/old'\nrsync -a %s/ OLD_HOST:rhumb-migration/old/\n```\n\nDo this before applying the plan: once applied, the inventory no longer has the old node to export. Keep the trailing `/` on the rsync source, or the bundle lands one directory deeper than the paths below.\n", shellQuote(r.OldID), shellQuote(p.OldBundle), shellQuote(p.OldBundle))
	b.WriteString("\nOn the old host, record what runs, then stop each instance. Jobs stop before the services they read. Each block saves the container's mounts first: `uninstall.sh` removes the container, and with it Docker's record of where the data lives.\n\n```sh\nmkdir -p \"$M/data\"\ndocker ps --format '{{.Names}}\\t{{.Status}}' > \"$M/data/docker-ps.txt\"\n```\n")
	for _, run := range p.Old {
		if !run.Script {
			continue
		}
		fmt.Fprintf(b, "\n%s%s\n\n```sh\nmkdir -p \"$M/data/%s\"\n", label(run), migrationJobLabel(run), run.Instance)
		for _, container := range run.Containers {
			// A container that was never created, or already removed, has
			// no mounts to record; say so rather than fail on an empty ID.
			fmt.Fprintf(b, "id=$(docker ps -aq --filter %s); [ -n \"$id\" ] && docker inspect -f %s \"$id\" > \"$M/data/%s/%s.mounts\" || echo 'no container: %s'\n", shellQuote(container.Filter), migrationMountsFormat, run.Instance, container.Name, container.Name)
		}
		for _, service := range run.OnDemand {
			fmt.Fprintf(b, "# %s runs only on demand (profiles: %s): no container to inspect\n", service.Service, service.Profiles)
		}
		if run.Problem != "" {
			fmt.Fprintf(b, "# containers unknown (%s): save `docker inspect` of each by hand\n", run.Problem)
		}
		fmt.Fprintf(b, "(cd \"%s\" && ./uninstall.sh)\n```\n", oldDir(run))
	}
	if len(p.Old) == 0 {
		b.WriteString("\nNo managed runtime instance runs on this node.\n")
	}
	b.WriteString("\n")
	for _, run := range p.Old {
		if !run.Script {
			fmt.Fprintf(b, "- [ ] %s: %s. Stop it with its own service manager.\n", label(run), run.Problem)
		}
	}
	b.WriteString("- [ ] `docker ps` lists none of the stopped containers.\n- [ ] Check what each `uninstall.sh` left behind. Each prints a `Preserved:` line naming what it did not delete. Phase 2 archives container mounts only, so compare each path on that line with the mount sources:\n\n  ```sh\n  cut -d'|' -f2 \"$M\"/data/*/*.mounts | sort -u\n  ```\n\n  A path in this list is archived in phase 2. A path not in it, such as a deploy key under `~/.ssh` or a web root outside the container, is not; ")
	if replace {
		b.WriteString("copy it to the new host yourself, or the new host starts without it.\n")
	} else {
		b.WriteString("it stays where it is on this machine, so a copy is only a safeguard against a damaged disk.\n")
	}

	// Phase 2.
	b.WriteString("\n### Phase 2 — Back up data (old host)\n\nArchive every mount under its path inside the container, which is the same on both sides.")
	if replace {
		b.WriteString(" A bind mount of large shared storage (media, home directories) can be left out when the new host mounts the same storage.\n")
	} else {
		b.WriteString(" Nothing here is deleted; this archive is what you restore from if the move damages a disk.\n")
	}
	b.WriteString("\nCheck sizes first, then delete the lines for anything you will not carry:\n\n```sh\ncat \"$M\"/data/*/*.mounts | cut -d'|' -f2 | sort -u | sudo xargs -r du -sh\n```\n")
	backups := 0
	for _, run := range p.Old {
		if len(run.Mounts) == 0 {
			continue
		}
		backups++
		fmt.Fprintf(b, "\n%s\n\n```sh\ncd \"$M/data/%s\"\n", label(run), run.Instance)
		for _, mount := range run.Mounts {
			fmt.Fprintf(b, "sudo tar -C \"$(awk -F'|' '$3==\"%s\"{print $2}' *.mounts)\" -czpf %s .  # %s\n", mount.Target, migrationArchiveName(mount.Target), mount.Type)
		}
		b.WriteString("```\n")
	}
	if backups == 0 {
		b.WriteString("\nNo container mount is known. Back up each service's data by hand.\n")
	}
	for _, run := range p.Old {
		for _, service := range run.OnDemand {
			if len(service.Mounts) != 0 {
				fmt.Fprintf(b, "\n- [ ] %s: on-demand service %s alone mounts %s, which no `.mounts` record locates. Find the source in its `compose.yaml` under `$M/old` and archive it by hand.\n", label(run), mdCode(service.Service), migrationInline(service.Mounts))
			}
		}
		if run.Script && run.Problem != "" {
			fmt.Fprintf(b, "\n- [ ] %s: mounts unknown (%s). Back it up by hand from its `.mounts` record.\n", label(run), run.Problem)
		}
	}
	b.WriteString("\nPack it, then copy the archive off this machine:\n\n```sh\nsudo tar -C ~ -czpf ~/rhumb-migration-data.tgz rhumb-migration/data\nsha256sum ~/rhumb-migration-data.tgz\n```\n")

	// Phase 3.
	b.WriteString("\n### Phase 3 — Manual work\n\n")
	if len(attention) != 0 {
		b.WriteString("- [ ] Resolve each item in [Needs attention](#2.%20Needs%20attention):\n")
		for i, item := range attention {
			fmt.Fprintf(b, "  - [ ] 2.%d %s\n", i+1, item.title)
		}
	}
	if replace {
		b.WriteString("- [ ] Bring up the new machine: install Docker, and create the external networks, mounts and directories the old host had. `$M/data/*/*.mounts` records what the old containers used.\n- [ ] Configure its interfaces so `ip -brief address` shows:\n")
	} else {
		b.WriteString("- [ ] Shut the machine down, move it and boot it. Configure its interfaces so `ip -brief address` shows:\n")
	}
	for _, address := range p.NewAddresses {
		fmt.Fprintf(b, "  - %s\n", mdCode(address))
	}
	fmt.Fprintf(b, "- [ ] On the workstation, apply the inventory change: run the plan with `rhumb migrate node ... --apply`, or open it in a front end such as the `dgs conf` Migrate tab. Then check it and export the new bundle:\n\n  ```sh\n  rhumb check\n  rhumb export node:%s --to %s --yes\n  ssh NEW_HOST 'mkdir -p ~/rhumb-migration/new'\n  rsync -a %s/ NEW_HOST:rhumb-migration/new/\n  ```\n\n",
		shellQuote(r.NewID), shellQuote(p.NewBundle), shellQuote(p.NewBundle))
	b.WriteString("  Keep the `.rhumb-migration-backup-*` path the apply result prints; it is the inventory rollback.\n")
	if replace {
		b.WriteString("- [ ] Copy `~/rhumb-migration-data.tgz` to the new host, compare its `sha256sum`, and unpack it: `tar -C ~ -xzpf ~/rhumb-migration-data.tgz`.\n")
	}
	b.WriteString("- [ ] Review each generated `compose.yaml` and configuration file in the new bundle before running anything.\n")

	// Phase 4.
	b.WriteString("\n### Phase 4 — Import data (new host)\n\n")
	imports := 0
	if replace {
		b.WriteString("`install.sh` creates the volumes and starts the container. Stop it at once, before it fills an empty data set, then unpack each archive into the new mount with the same container path.\n")
		for _, run := range p.New {
			old := migrationOldRuntime(p, run.Instance)
			if old == nil || len(old.Mounts) == 0 || !run.Script || len(run.Containers) == 0 {
				continue
			}
			imports++
			fmt.Fprintf(b, "\n%s\n\n```sh\n(cd \"%s\" && ./install.sh)\ndocker stop %s\ncd \"$M/data/%s\"\n", label(run), newDir(run), migrationRefs(run.Containers), old.Instance)
			for _, container := range run.Containers {
				fmt.Fprintf(b, "docker inspect -f %s %s > %s.new-mounts\n", migrationMountsFormat, container.Ref, container.Name)
			}
			for _, mount := range old.Mounts {
				fmt.Fprintf(b, "sudo tar -C \"$(awk -F'|' '$3==\"%s\"{print $2}' *.new-mounts)\" -xzpf %s\n", mount.Target, migrationArchiveName(mount.Target))
			}
			b.WriteString("```\n")
		}
	}
	if !replace {
		b.WriteString("Nothing to import: the data is still on this machine. Confirm the volumes survived the move:\n\n```sh\ndocker volume ls\n```\n")
	} else if imports == 0 {
		b.WriteString("\nNo container mount to import.\n")
	}

	// Phase 5.
	b.WriteString("\n### Phase 5 — Start services (new host)\n\nListening services start before the jobs that read them.\n")
	for _, run := range p.New {
		if !run.Script {
			continue
		}
		fmt.Fprintf(b, "\n%s%s\n\n```sh\n", label(run), migrationJobLabel(run))
		if old := migrationOldRuntime(p, run.Instance); replace && old != nil && len(old.Mounts) != 0 && len(run.Containers) != 0 {
			fmt.Fprintf(b, "docker start %s  # installed in phase 4\n", migrationRefs(run.Containers))
		} else {
			fmt.Fprintf(b, "(cd \"%s\" && ./install.sh)\n", newDir(run))
		}
		b.WriteString("```\n")
	}
	b.WriteString("\n")
	for _, run := range p.New {
		if !run.Script {
			fmt.Fprintf(b, "- [ ] %s: %s. Start it with its own service manager.\n", label(run), run.Problem)
		}
	}
	r.writeOtherTargets(b)

	// Phase 6.
	b.WriteString("\n### Phase 6 — Verify\n\nOn the new host:\n\n")
	for _, run := range p.New {
		for _, container := range run.Containers {
			fmt.Fprintf(b, "- [ ] %s is up and its log is clean: `docker ps --filter %s`, `docker logs --tail 50 %s`\n", mdCode(container.Name), container.Filter, container.Ref)
		}
		if len(run.Containers) == 0 {
			fmt.Fprintf(b, "- [ ] %s runs\n", label(run))
		}
	}
	if len(p.Probe) != 0 {
		b.WriteString("\nFrom a client on each network, every address a route dials answers:\n\n| Network | Command | Instance | Route |\n|---|---|---|---|\n")
		for _, probe := range p.Probe {
			network := probe.Network
			if network == "" {
				network = "loopback, on the host"
			}
			fmt.Fprintf(b, "| %s | `nc -vz %s %d` | %s | %s |\n", network, probe.Address, probe.Port, mdCode(probe.Instance), mdCode(probe.Route))
		}
	}
	if len(r.DNS) != 0 {
		b.WriteString("\nDNS, from the networks clients use; section 2 names the expected ingress:\n\n")
		for _, item := range r.DNS {
			fmt.Fprintf(b, "- [ ] `dig +short %s`\n", item.Name)
		}
	}
	b.WriteString("\n- [ ] Each job ran once and wrote what it should; check its logs.\n- [ ] Data is complete: log in, open recent items, compare counts with `$M/data/docker-ps.txt` and what you remember of the old host.\n- [ ] `rhumb check` reports nothing unexpected.\n")

	b.WriteString("\n### Rollback\n\n1. Stop the new services with their `uninstall.sh`.\n2. Restore the inventory from the `.rhumb-migration-backup-*` directory the apply result printed.\n")
	if replace {
		b.WriteString("3. Bring the old machine back; its disks were not touched.\n4. Reinstall from the old bundle in `$M/old`.\n")
	} else {
		b.WriteString("3. Restore the old network settings.\n4. Reinstall from the old bundle in `$M/old`; if a disk was damaged, import `~/rhumb-migration-data.tgz` first as in the replace procedure.\n")
	}
	b.WriteString("5. Revert any DNS change you made.\n\nRemove `~/rhumb-migration`, the data archive and retained old secret paths only once rollback is no longer needed.\n")
}

func (r *migrationReport) writeOtherTargets(b *bytes.Buffer) {
	var lines []string
	for _, item := range r.Work {
		owner := item.Target.Node
		if owner == "" {
			owner = item.Target.User
		}
		if owner == r.OldID || owner == r.NewID {
			continue
		}
		label := mdCode(item.Target.Service + "/" + item.Target.Instance)
		bundle := "migration-other-" + owner
		var line string
		switch {
		case !item.Present:
			line = fmt.Sprintf("- [ ] %s on %s: retire the old output once its replacement works.", label, mdCode(owner))
		case item.Target.Export != "":
			line = fmt.Sprintf("- [ ] %s: `rhumb export instance:%s --to %s --yes`, then hand the files to %s.", label, shellQuote(item.Target.Instance), shellQuote(bundle), mdCode(owner))
		default:
			line = fmt.Sprintf("- [ ] %s on %s: `rhumb export instance:%s --to %s --yes`, copy it there and run its `install.sh`.", label, mdCode(owner), shellQuote(item.Target.Instance), shellQuote(bundle))
		}
		if item.Unknown {
			line += " Render not compared: inspect it first."
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("\nOther machines whose files change — once the new host works:\n\n" + strings.Join(lines, "\n") + "\n")
}

func migrationOldRuntime(p migrationProcedure, newInstance string) *migrationRuntime {
	for i := range p.Old {
		if p.Pair[p.Old[i].Instance] == newInstance {
			return &p.Old[i]
		}
	}
	return nil
}

func migrationJobLabel(run migrationRuntime) string {
	if run.Job {
		return " (job)"
	}
	return ""
}

func migrationRefs(containers []migrationContainer) string {
	refs := make([]string, len(containers))
	for i, container := range containers {
		refs[i] = container.Ref
	}
	return strings.Join(refs, " ")
}

func migrationInline(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return mdCode(strings.Join(values, ", "))
}

func migrationListenerless(l engine.Loaded, nodeID, instanceID string) bool {
	for _, node := range l.Inv.Nodes {
		if node.ID != nodeID {
			continue
		}
		for _, instance := range node.Instances {
			if instance.ID == instanceID {
				return len(instance.Ports) == 0
			}
		}
	}
	return false
}

func migrationHasInstanceDeployScript(l engine.Loaded, item target.Target, name string) bool {
	containerised := false
	for _, node := range l.Inv.Nodes {
		if node.ID != item.Node {
			continue
		}
		for _, instance := range node.Instances {
			if instance.ID == item.Instance {
				containerised = instance.Containerised()
				break
			}
		}
	}
	if !containerised {
		return false
	}
	for _, file := range l.Deploys[item.Service].Files {
		if file.Output == name {
			return true
		}
	}
	return false
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
