package validate

import (
	"strings"
	"testing"
	"time"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

// validInventory is a small, clean inventory: one server node with two
// ports on one instance, one managed client node, one unmanaged user, and a
// two-hop relay route so rule 8's successor check has something to pin.
func validInventory() *inventory.Root {
	return &inventory.Root{
		Nodes: []inventory.Node{
			{
				ID:       "srv",
				Networks: inventory.Networks{"internet": "203.0.113.10"},
				Instances: []inventory.Instance{
					{ID: "ss-srv", Service: "ssserver", Ports: inventory.PortsOf(map[string]int{"main": 38250, "alt": 49217})},
				},
			},
			{
				ID:       "relay",
				Networks: inventory.Networks{"internet": "203.0.113.20"},
				Instances: []inventory.Instance{
					{ID: "ss-relay", Service: "ssserver", Ports: inventory.PortsOf(map[string]int{"main": 40000})},
				},
			},
			{
				ID:      "laptop",
				Owner:   "dana",
				Reaches: []string{"home"},
				Instances: []inventory.Instance{
					{ID: "laptop-sea-ssserver-ss-json", Ports: inventory.PortsOf(map[string]int{"local": 1080})},
				},
			},
		},
		Users: map[string]inventory.User{
			"dana": {Username: "dana", Access: []string{"sea"}},
			"yak":  {Username: "yak", Devices: inventory.DevicesNone, Access: []string{"sea"}},
		},
		Routes: map[string]inventory.Route{
			"sea":   {Hops: []string{"ss-srv:main"}},
			"chain": {Hops: []string{"ss-relay:main", "ss-srv:alt"}},
		},
		Networks:  []string{"home", "internet"},
		Universal: "internet",
	}
}

func validManifests() map[string]confgen.Manifest {
	return map[string]confgen.Manifest{
		"ssserver": {Auth: confgen.AuthPerPrincipal, Exports: []string{"ss-json"}, Template: "t"},
		"ss-json":  {Auth: confgen.AuthNone, Template: "t"},
	}
}

// validExports is the exports the fixture's services name, each a rendering
// and nothing more.
func validExports() map[string]confgen.Export {
	return map[string]confgen.Export{
		"ss-link": {Template: "t", Output: "share.txt"},
		"ss-json": {Template: "t", Output: "config.json"},
	}
}

func derived(t *testing.T, inv *inventory.Root, manifests map[string]confgen.Manifest) *derive.Model {
	t.Helper()
	m, err := derive.Derive(inv, manifests)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return m
}

func messages(issues []Issue) []string {
	out := make([]string, len(issues))
	for i, iss := range issues {
		out[i] = iss.Message
	}
	return out
}

func containsSubstring(issues []Issue, substr string) bool {
	for _, iss := range issues {
		if strings.Contains(iss.Message, substr) {
			return true
		}
	}
	return false
}

func TestValidate_CleanInventoryHasNoIssues(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if len(got) != 0 {
		t.Fatalf("Validate = %v, want none", messages(got))
	}
}

func TestValidate_DuplicateInstanceID(t *testing.T) {
	inv := validInventory()
	inv.Nodes[1].Instances[0].ID = "ss-srv" // collides with the srv node's instance.
	manifests := validManifests()
	// Derive itself refuses a duplicate instance ID before it can compute
	// anything, so this checks Validate's own pass over the inventory
	// directly rather than routing through a Derive that would never reach
	// it in practice.
	got := Validate(inv, manifests, validExports(), &derive.Model{}, nil)
	if !containsSubstring(got, `instance "ss-srv" is defined more than once, in node `) {
		t.Fatalf("Validate = %v, want a duplicate-instance issue", messages(got))
	}
}

func TestValidate_UnknownServiceAndRole(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Instances[0].Service = "nonesuch"
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `service "nonesuch" is not defined`) {
		t.Fatalf("Validate = %v, want an unknown-service issue", messages(got))
	}
}

func TestValidate_UnknownService(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Instances[0].Service = "nonesuch"
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `service "nonesuch" is not defined`) {
		t.Fatalf("Validate = %v, want an unknown-service issue", messages(got))
	}
}

// A `export` naming a form none of the services this device reaches offers
// writes nothing at all, silently. There is no separate "not in exports/"
// case: a service's exports are the directories it holds, so a name the
// service does not offer is the only way to get this wrong.
func TestValidate_NodeExportNotInExportsDir(t *testing.T) {
	inv := validInventory()
	inv.Nodes[2].Export = "nonesuch"
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `node "laptop": export "nonesuch" is not one of the ways ssserver is written out`) {
		t.Fatalf("Validate = %v, want a bad export issue", messages(got))
	}
}

func TestValidate_HopNamesMissingInstanceOrPort(t *testing.T) {
	inv := validInventory()
	inv.Routes["broken-instance"] = inventory.Route{Hops: []string{"nonesuch:main"}}
	inv.Routes["broken-port"] = inventory.Route{Hops: []string{"ss-srv:nonesuch"}}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `names an instance that does not exist`) {
		t.Fatalf("Validate = %v, want a missing-instance issue", messages(got))
	}
	if !containsSubstring(got, `has no port "nonesuch"`) {
		t.Fatalf("Validate = %v, want a missing-port issue", messages(got))
	}
}

func TestValidate_ReservedSelfPort(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Instances[0].Ports["self"] = inventory.Port{Number: 1234}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `port "self" is reserved`) {
		t.Fatalf("Validate = %v, want a reserved-port issue", messages(got))
	}
}

func TestValidate_UnknownRouteInAccessAndUnknownOwner(t *testing.T) {
	inv := validInventory()
	inv.Users["dana"] = inventory.User{Username: "dana", Access: []string{"sea", "nonesuch"}}
	inv.Nodes[2].Owner = "nonesuch"
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `access names route "nonesuch"`) {
		t.Fatalf("Validate = %v, want an unknown-route issue", messages(got))
	}
	if !containsSubstring(got, `owner "nonesuch" is not a user`) {
		t.Fatalf("Validate = %v, want an unknown-owner issue", messages(got))
	}
}

func TestValidate_OverrideMatchesNothingDerived(t *testing.T) {
	inv := validInventory()
	inv.Nodes[2].Instances[0].ID = "laptop-typo-route"
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `overrides nothing this node derives`) {
		t.Fatalf("Validate = %v, want an unmatched-override issue", messages(got))
	}
}

func TestValidate_OverrideSettingServiceOrRole(t *testing.T) {
	inv := validInventory()
	inv.Nodes[2].Instances[0].Service = "shadowsocks-rust"
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `sets service or role; an override may only set ports and bind`) {
		t.Fatalf("Validate = %v, want an invalid-override issue", messages(got))
	}
}

func TestValidate_DifferentSuccessorsForOneNonTerminalHop(t *testing.T) {
	inv := validInventory()
	inv.Nodes[1].Instances = append(inv.Nodes[1].Instances, inventory.Instance{
		ID: "ss-relay-b", Service: "ssserver", Ports: inventory.PortsOf(map[string]int{"main": 40001}),
	})
	inv.Routes["chain2"] = inventory.Route{Hops: []string{"ss-relay:main", "ss-relay-b:main"}}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `has different successors in routes`) {
		t.Fatalf("Validate = %v, want a rule-based-routing issue", messages(got))
	}
}

func TestValidate_RouteNamesOneInstanceTwice(t *testing.T) {
	inv := validInventory()
	inv.Routes["loop"] = inventory.Route{Hops: []string{"ss-relay:main", "ss-srv:alt", "ss-relay:main"}}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `names instance "ss-relay" twice`) {
		t.Fatalf("Validate = %v, want a repeated-instance issue", messages(got))
	}
}

func TestValidate_CarriedCredentialRouteNotEnteringOnInternet(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Networks = inventory.Networks{"home": "10.0.1.5"} // srv no longer has an internet address.
	// The relay->srv edge in "chain" would now fail to resolve too, which
	// Derive treats as fatal; this rule reads only the inventory, so check
	// it directly rather than through a Derive that would never get here.
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), &derive.Model{}, nil)
	if !containsSubstring(got, `user "yak": route "sea" enters "ss-srv", which has no address on a network reachable by their carried credential (tried: internet)`) {
		t.Fatalf("Validate = %v, want a carried-credential-not-on-internet issue", messages(got))
	}
}

func TestValidate_CarriedCredentialReachesPrivateNetwork(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Networks = inventory.Networks{"home": "10.0.1.5"}
	inv.Nodes[1].Reaches = []string{"home"} // relay still reaches srv for "chain".
	inv.Users["yak"] = inventory.User{
		Devices: inventory.DevicesNone, Access: []string{"sea"},
		Credentials: map[string]inventory.Credential{"default": {Reaches: []string{"home"}}},
	}
	// Derive must resolve the carried file on home too, not only validate it.
	model := derived(t, inv, validManifests())
	got := Validate(inv, validManifests(), validExports(), model, nil)
	if containsSubstring(got, `user "yak": route "sea" enters "ss-srv"`) {
		t.Fatalf("Validate = %v, want home reachability accepted", messages(got))
	}
	found := false
	for _, e := range model.Edges {
		if e.Route == "sea" && strings.HasPrefix(e.FromInstance, "yak-") {
			found = true
			if e.Network != "home" || e.Address != "10.0.1.5" {
				t.Fatalf("carried edge = %+v, want home 10.0.1.5", e)
			}
		}
	}
	if !found {
		t.Fatalf("Edges = %+v, want a carried sea edge for yak", model.Edges)
	}
}

func TestValidate_CarriedCredentialWithoutUniversalNetwork(t *testing.T) {
	inv := validInventory()
	inv.Universal = ""
	inv.Users["yak"] = inventory.User{Devices: inventory.DevicesNone, Access: []string{"sea"}}
	got := Validate(inv, validManifests(), validExports(), &derive.Model{}, nil)
	if !containsSubstring(got, `user "yak": route "sea" enters "ss-srv", which has no address on a network reachable by their carried credential`) {
		t.Fatalf("Validate = %v, want rule 12 checked without a universal network", messages(got))
	}
}

func TestValidate_ReachesOnCredentialADeviceNames(t *testing.T) {
	inv := validInventory()
	user := inv.Users["dana"]
	user.Credentials = map[string]inventory.Credential{"default": {Reaches: []string{"home"}}}
	inv.Users["dana"] = user
	got := Validate(inv, validManifests(), validExports(), &derive.Model{}, nil)
	if !containsSubstring(got, `user "dana": credential "default" declares reaches, but a device of theirs names it`) {
		t.Fatalf("Validate = %v, want reaches on a device-named credential reported", messages(got))
	}
}

func TestValidate_CredentialReachesUnknownNetwork(t *testing.T) {
	inv := validInventory()
	inv.Users["yak"] = inventory.User{
		Devices: inventory.DevicesNone, Access: []string{"sea"},
		Credentials: map[string]inventory.Credential{"default": {Reaches: []string{"unknown"}}},
	}
	got := Validate(inv, validManifests(), validExports(), derived(t, inv, validManifests()), nil)
	if !containsSubstring(got, `user "yak": credential "default" reaches unknown network "unknown"`) {
		t.Fatalf("Validate = %v, want unknown network issue", messages(got))
	}
}

func TestValidate_DuplicateAccountNameOnOnePort(t *testing.T) {
	inv := validInventory()
	// An account name is the username and the credential. A second person
	// whose username and credential spell the same pair collides on
	// ss-srv's main port, and the server's table would carry two rows a
	// reader cannot tell apart.
	inv.Users["clash"] = inventory.User{
		Username: "dana", Devices: inventory.DevicesNone, Access: []string{"sea"},
	}
	manifests := validManifests()
	model := derived(t, inv, manifests)
	got := Validate(inv, manifests, validExports(), model, nil)
	if !containsSubstring(got, `account "dana-default" is rendered by more than one principal`) {
		t.Fatalf("Validate = %v, want a duplicate-account issue", messages(got))
	}
}

func TestValidate_TwoInstancesBindTheSameAddressAndPort(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Instances = append(inv.Nodes[0].Instances, inventory.Instance{
		ID: "other-srv", Service: "ssserver", Ports: inventory.PortsOf(map[string]int{"main": 38250}),
	})
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `bind the same address, port and protocol`) {
		t.Fatalf("Validate = %v, want a bind-collision issue", messages(got))
	}
}

// TestValidate_SamePortDifferentProtocolIsNotACollision covers the case the
// check exists to allow: a QUIC service on 443/udp beside a web server on
// 443/tcp is two real listeners, not one machine bound twice.
func TestValidate_SamePortDifferentProtocolIsNotACollision(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Instances = append(inv.Nodes[0].Instances, inventory.Instance{
		ID: "quic-srv", Service: "ssserver",
		Ports: inventory.Ports{"main": {Number: 38250, Protocol: inventory.ProtocolUDP}},
	})
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, `bind the same address, port and protocol`) {
		t.Fatalf("Validate = %v, want no collision: one is udp and one is tcp", messages(got))
	}
}

func TestValidate_UniversalNotInNetworksList(t *testing.T) {
	inv := validInventory()
	inv.Universal = "nonesuch"
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), &derive.Model{}, nil)
	if !containsSubstring(got, `universal "nonesuch" does not name a network in the list`) {
		t.Fatalf("Validate = %v, want a bad-universal issue", messages(got))
	}
}

func TestValidate_UserExportNotInExportsDir(t *testing.T) {
	inv := validInventory()
	yak := inv.Users["yak"]
	yak.Export = "nonesuch"
	inv.Users["yak"] = yak
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `user "yak": export "nonesuch" is not one of the ways ssserver is written out`) {
		t.Fatalf("Validate = %v, want a bad export issue", messages(got))
	}
}

func TestValidate_StalePrevious(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()
	previous := map[string]time.Time{
		"ss-srv/main/node/laptop": time.Now().Add(-8 * 24 * time.Hour),
	}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), previous)
	if !containsSubstring(got, `ss-srv/main/node/laptop.previous`) || !containsSubstring(got, "seven-day limit") {
		t.Fatalf("Validate = %v, want a stale-.previous issue", messages(got))
	}
}

func TestValidate_RecentPreviousIsNotAnIssue(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()
	previous := map[string]time.Time{
		"ss-srv/main/node/laptop": time.Now().Add(-6 * 24 * time.Hour),
	}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), previous)
	if containsSubstring(got, "seven-day limit") {
		t.Fatalf("Validate = %v, want no stale-.previous issue for a six-day-old file", messages(got))
	}
}

// TestValidate_PortHandsOutUnknownSecret covers a port handing out a name
// the service never declared: a value the client never receives, with
// nothing in the rendered file to say it was meant to be there.
func TestValidate_PortHandsOutUnknownSecret(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()
	ss := manifests["ssserver"]
	ss.Self = confgen.SelfDecls{"psk": {Set: true}}
	manifests["ssserver"] = ss

	inst := &inv.Nodes[0].Instances[0]
	port := inst.Ports["main"]
	port.Self = []string{"nonesuch.main"}
	inst.Ports["main"] = port

	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `hands out "nonesuch"`) {
		t.Fatalf("Validate = %v, want a port handing out an undeclared secret", messages(got))
	}

	port.Self = []string{"psk.main"}
	inst.Ports["main"] = port
	got = Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, "hands out") {
		t.Fatalf("Validate = %v, want no issue once the name is declared", messages(got))
	}
}

// TestValidate_PortNamesASetWithoutItsKey covers the two halves of the same
// mistake: a set handed out without saying which of its values, and a
// single value handed out with a key it does not have.
func TestValidate_PortNamesASetWithoutItsKey(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()
	ss := manifests["ssserver"]
	ss.Self = confgen.SelfDecls{"psk": {Set: true}, "tls_key": {}}
	manifests["ssserver"] = ss

	inst := &inv.Nodes[0].Instances[0]
	port := inst.Ports["main"]
	port.Self = []string{"psk"}
	inst.Ports["main"] = port
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, "which is a set: name one of its keys") {
		t.Fatalf("Validate = %v, want a set named without a key", messages(got))
	}

	port.Self = []string{"tls_key.main"}
	inst.Ports["main"] = port
	got = Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, "is one value and takes no key") {
		t.Fatalf("Validate = %v, want a single value named with a key", messages(got))
	}
}

// TestValidate_InstanceSelfOutsideTheServiceList covers the one way an
// instance's narrowed `self` can be wrong in the other direction: a name the
// service never declared is a credential sync would generate and nothing
// accounts for.
func TestValidate_InstanceSelfOutsideTheServiceList(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()
	ss := manifests["ssserver"]
	ss.Self = confgen.SelfDecls{"psk": {Set: true}}
	manifests["ssserver"] = ss
	inv.Nodes[0].Instances[0].Self = map[string][]string{"psk": {"main"}, "nonesuch": nil}

	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `self "nonesuch" is not one of "ssserver"'s own secrets`) {
		t.Fatalf("Validate = %v, want a self outside the service's list issue", messages(got))
	}
}

// TestValidate_InstanceSelfKeysOnASingleValue covers keys written against a
// name the service declares as one value: they name files sync neither
// generates nor reports.
func TestValidate_InstanceSelfKeysOnASingleValue(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()
	ss := manifests["ssserver"]
	ss.Self = confgen.SelfDecls{"tls_key": {}}
	manifests["ssserver"] = ss
	inv.Nodes[0].Instances[0].Self = map[string][]string{"tls_key": {"main"}}

	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, "takes no keys") {
		t.Fatalf("Validate = %v, want a keyed single value issue", messages(got))
	}
}

// `devices: none` asserts there is no node file for this person. One owned
// by them says the opposite, and the pair is what tells a deliberate absence
// apart from a misplaced file.
func TestValidate_DevicesNoneBesideANodeFile(t *testing.T) {
	inv := validInventory()
	yak := inv.Users["yak"]
	yak.Devices = inventory.DevicesNone
	inv.Users["yak"] = yak
	inv.Nodes = append(inv.Nodes, inventory.Node{ID: "yak-phone", Owner: "yak"})

	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `user "yak": devices: none says this inventory holds no node file for them, but yak-phone is theirs`) {
		t.Fatalf("Validate = %v, want the contradiction named", messages(got))
	}
}

// A credential narrows the routes its owner already holds. Naming one they do
// not hold would grant access from the wrong place — access belongs to the
// person — so it is reported with the routes they do have.
func TestValidate_CredentialNarrowsToARouteItsOwnerDoesNotHold(t *testing.T) {
	inv := validInventory()
	inv.Users["dana"] = inventory.User{
		Username: "dana",
		Access:   []string{"sea"},
		Credentials: map[string]inventory.Credential{
			"default": {},
			"mbp":     {Note: "the laptop", Access: []string{"nonesuch"}},
		},
	}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `credential "mbp" names route "nonesuch"`) {
		t.Fatalf("Validate = %v, want a credential narrowed past its owner's access", messages(got))
	}
}

// Narrowing to a route the person does hold is ordinary and reports nothing.
func TestValidate_CredentialNarrowedWithinItsOwnersAccessIsFine(t *testing.T) {
	inv := validInventory()
	user := inv.Users["dana"]
	user.Credentials = map[string]inventory.Credential{
		"default": {},
		"mbp":     {Note: "the laptop", Access: []string{user.Access[0]}},
	}
	inv.Users["dana"] = user
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, `credential "mbp"`) {
		t.Fatalf("Validate = %v, want nothing reported for a credential within its owner's access", messages(got))
	}
}

// TestValidate_UpstreamSharedFromAPortThatHandsOutNothing covers rule 17.
// Needing the hop's shared secrets is the dialling service's own
// declaration, so nothing about the port it reaches makes it true. A port
// handing out none renders an empty list and a credential built half from
// it, which authenticates nothing and looks complete in the file.
func TestValidate_UpstreamSharedFromAPortThatHandsOutNothing(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()
	ss := manifests["ssserver"]
	ss.Self = confgen.SelfDecls{"psk": {Set: true}}
	ss.Upstream = confgen.UpstreamDecls{confgen.UpstreamShared: {}}
	manifests["ssserver"] = ss

	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `instance "ss-relay" needs the shared secrets of ss-srv:alt`) {
		t.Fatalf("Validate = %v, want the relay's unmet need named", messages(got))
	}

	// The port it dials now hands one out, which is the only thing that
	// can settle it.
	inst := &inv.Nodes[0].Instances[0]
	port := inst.Ports["alt"]
	port.Self = []string{"psk.alt"}
	inst.Ports["alt"] = port
	got = Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, "needs the shared secrets") {
		t.Fatalf("Validate = %v, want no issue once the port hands one out", messages(got))
	}
}

// TestValidate_RouteEndingOnAForwarderIsReported covers rule 21. A relay
// terminates nothing, so a route ending on one ends nowhere: the client
// granted it would be handed an address and no account, because the account
// belongs to the hop that ends the chain and there is none.
func TestValidate_RouteEndingOnAForwarderIsReported(t *testing.T) {
	inv := validInventory()
	inv.Nodes[1].Instances[0].Service = "realm"
	manifests := validManifests()
	manifests["realm"] = confgen.Manifest{Auth: confgen.AuthNone, Forwards: true, Template: "t"}
	inv.Routes["dead"] = inventory.Route{Hops: []string{"ss-relay:main"}}

	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `route "dead" ends on "ss-relay", which forwards`) {
		t.Fatalf("Validate = %v, want the route that terminates nowhere named", messages(got))
	}
	// The chain through the same relay ends on a server and is ordinary.
	if containsSubstring(got, `route "chain" ends on`) {
		t.Fatalf("Validate = %v, want nothing said about a relay with a hop after it", messages(got))
	}
}

// TestValidate_PortDispatchKeepsOneSuccessorPerPort covers rule 22.
// `downstreams: many` lifts rule 8 for the instance as a whole; an instance
// that tells its routes apart by the port they arrived on still has one next
// hop per port, and two routes disagreeing about it would render two
// endpoints listening on one port and going to different places.
func TestValidate_PortDispatchKeepsOneSuccessorPerPort(t *testing.T) {
	inv := validInventory()
	inv.Nodes[1].Instances[0].Service = "realm"
	inv.Nodes[1].Instances[0].Ports = inventory.PortsOf(map[string]int{"main": 40000, "other": 40001})
	manifests := validManifests()
	manifests["realm"] = confgen.Manifest{
		Auth: confgen.AuthNone, Forwards: true, Template: "t",
		Downstreams: confgen.DownstreamsMany, Dispatch: confgen.DispatchPort,
	}
	inv.Routes["chain"] = inventory.Route{Hops: []string{"ss-relay:main", "ss-srv:alt"}}
	inv.Routes["clash"] = inventory.Route{Hops: []string{"ss-relay:main", "ss-srv:main"}}

	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `its port "main" has different successors in routes`) {
		t.Fatalf("Validate = %v, want one port's two successors named", messages(got))
	}
	// A downstream of a port-dispatching instance needs no published name:
	// what tells the routes apart is the port they arrived on.
	if containsSubstring(got, "declares no published name") {
		t.Fatalf("Validate = %v, want no published name asked of a relay's downstream", messages(got))
	}

	// The same relay with one route per port is ordinary.
	inv.Routes["clash"] = inventory.Route{Hops: []string{"ss-relay:other", "ss-srv:main"}}
	got = Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, "different successors") {
		t.Fatalf("Validate = %v, want nothing reported for one successor per port", messages(got))
	}
}

// TestValidate_UpstreamSharedOptionalFromAPortThatHandsOutNothing covers the
// declaration that says the hop may hand over nothing. One program is
// configured both ways on different machines — a Hysteria2 instance that
// obfuscates its handshake hands out an obfuscation password, and one that
// does not hands out nothing and is still reachable — so the unmet need this
// rule reports is not a mistake there.
func TestValidate_UpstreamSharedOptionalFromAPortThatHandsOutNothing(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()
	ss := manifests["ssserver"]
	ss.Self = confgen.SelfDecls{"psk": {Set: true}}
	ss.Upstream = confgen.UpstreamDecls{confgen.UpstreamShared: {Optional: true}}
	manifests["ssserver"] = ss

	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, "needs the shared secrets") {
		t.Fatalf("Validate = %v, want nothing owed for an optional declaration", messages(got))
	}
}

// TestValidate_UpstreamSharedUndeclaredIsNotAnIssue is the other half: a
// service that never asked is not owed anything, however little the hop it
// dials hands out. A reverse proxy in front of a web service is this case.
func TestValidate_UpstreamSharedUndeclaredIsNotAnIssue(t *testing.T) {
	inv := validInventory()
	manifests := validManifests()

	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, "needs the shared secrets") {
		t.Fatalf("Validate = %v, want nothing owed to a service that did not ask", messages(got))
	}
}

// fanOutInventory puts a proxy in front of two web services, each on its own
// route, which is the shape rule 8 used to reject outright.
func fanOutInventory() (*inventory.Root, map[string]confgen.Manifest) {
	inv := validInventory()
	inv.Nodes[0].Instances = append(inv.Nodes[0].Instances,
		inventory.Instance{ID: "proxy", Service: "caddy", Ports: inventory.PortsOf(map[string]int{"https": 443})},
		inventory.Instance{ID: "vault", Service: "web", Ports: inventory.Ports{
			"web": {Number: 8222, Published: "vault.example.com"},
		}},
		inventory.Instance{ID: "bin", Service: "web", Ports: inventory.Ports{
			"web": {Number: 8080, Published: "clip.example.com"},
		}},
	)
	inv.Routes["vault"] = inventory.Route{Hops: []string{"proxy:https", "vault:web"}}
	inv.Routes["clip"] = inventory.Route{Hops: []string{"proxy:https", "bin:web"}}

	manifests := validManifests()
	manifests["caddy"] = confgen.Manifest{Auth: confgen.AuthNone, Downstreams: confgen.DownstreamsMany, Template: "t"}
	manifests["web"] = confgen.Manifest{Auth: confgen.AuthNone, Template: "t"}
	return inv, manifests
}

func TestValidate_FanOutIsNotRuleBasedRouting(t *testing.T) {
	inv, manifests := fanOutInventory()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if len(got) != 0 {
		t.Fatalf("Validate = %v, want none", messages(got))
	}
}

// Without the declaration the same two routes are rule 8's original error,
// and the message points at the declaration rather than leaving a reader to
// hunt for a rule set they never wrote.
func TestValidate_TwoSuccessorsWithoutFanOut(t *testing.T) {
	inv, manifests := fanOutInventory()
	manifests["caddy"] = confgen.Manifest{Auth: confgen.AuthNone, Template: "t"}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `instance "proxy" has different successors in routes "clip" and "vault"`) {
		t.Fatalf("Validate = %v, want a successor issue", messages(got))
	}
	if !containsSubstring(got, "downstreams: many") {
		t.Fatalf("Validate = %v, want the error to name the declaration", messages(got))
	}
}

// Rule 17: a downstream with no published name leaves the proxy nothing to
// match a site block on.
func TestValidate_FanOutDownstreamWithoutPublished(t *testing.T) {
	inv, manifests := fanOutInventory()
	inv.Nodes[0].Instances[2].Ports["web"] = inventory.Port{Number: 8222}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `instance "proxy" reaches vault:web in route "vault", which declares no published name`) {
		t.Fatalf("Validate = %v, want a missing-published issue", messages(got))
	}
}

// Rule 18: a proxy tells its downstreams apart by name alone, so two ports
// behind it cannot share one.
func TestValidate_PublishedNameSharedBehindAProxy(t *testing.T) {
	inv, manifests := fanOutInventory()
	inv.Nodes[0].Instances[3].Ports["web"] = inventory.Port{Number: 8080, Published: "vault.example.com"}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `published name "vault.example.com" is declared by bin:web and vault:web, and a proxy in front of one cannot tell them apart`) {
		t.Fatalf("Validate = %v, want a duplicate-published issue", messages(got))
	}
}

// Rule 18: two ports no proxy fronts may share a name, as two services on one
// machine share its DNS name, as long as their number or transport differs.
func TestValidate_PublishedNameSharedOnDifferentPorts(t *testing.T) {
	inv, manifests := fanOutInventory()
	inv.Routes = map[string]inventory.Route{}
	inv.Nodes[0].Instances[2].Ports["web"] = inventory.Port{Number: 8222, Published: "host.example.com"}
	inv.Nodes[0].Instances[3].Ports["web"] = inventory.Port{Number: 8222, Protocol: inventory.ProtocolUDP, Published: "host.example.com"}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, `published name "host.example.com"`) {
		t.Fatalf("Validate = %v, want no duplicate-published issue", messages(got))
	}
}

// Rule 18: a name resolves to one machine, so ports on two nodes cannot share
// it even on different numbers — a client of the second would dial the first.
func TestValidate_PublishedNameSharedAcrossNodes(t *testing.T) {
	inv, manifests := fanOutInventory()
	inv.Routes = map[string]inventory.Route{}
	bin := inv.Nodes[0].Instances[3]
	inv.Nodes[0].Instances = inv.Nodes[0].Instances[:3]
	bin.Ports = inventory.Ports{"web": {Number: 443, Protocol: inventory.ProtocolUDP, Published: "host.example.com"}}
	inv.Nodes[0].Instances[2].Ports["web"] = inventory.Port{Number: 8222, Published: "host.example.com"}
	other := inv.Nodes[0]
	other.ID, other.Instances = "other", []inventory.Instance{bin}
	inv.Nodes = append(inv.Nodes, other)
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `published name "host.example.com" is declared by bin:web on other and vault:web on `+inv.Nodes[0].ID+`, and one name reaches one machine`) {
		t.Fatalf("Validate = %v, want a duplicate-published issue", messages(got))
	}
}

// Rule 18: on one number and transport, nobody dialing the name can tell the
// two ports apart.
func TestValidate_PublishedNameSharedOnOnePort(t *testing.T) {
	inv, manifests := fanOutInventory()
	inv.Routes = map[string]inventory.Route{}
	inv.Nodes[0].Instances[2].Ports["web"] = inventory.Port{Number: 8222, Published: "host.example.com"}
	inv.Nodes[0].Instances[3].Ports["web"] = inventory.Port{Number: 8222, Published: "host.example.com"}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `published name "host.example.com" is declared by bin:web and vault:web, both on 8222/tcp`) {
		t.Fatalf("Validate = %v, want a duplicate-published issue", messages(got))
	}
}

func TestValidate_Profiles(t *testing.T) {
	manifests := validManifests()
	cases := []struct {
		name string
		edit func(n *inventory.Node)
		want string
	}{
		{"export beside profiles", func(n *inventory.Node) {
			n.Export = "ss-json"
			n.Profiles = map[string]inventory.Profile{"singbox": {}}
		}, `node "laptop": export and profiles are not written together`},
		{"export nobody offers", func(n *inventory.Node) {
			n.Profiles = map[string]inventory.Profile{"singbox": {Export: "nonesuch"}}
		}, `node "laptop": profile "singbox": export "nonesuch" is not one of the ways ssserver is written out`},
		{"export none", func(n *inventory.Node) {
			n.Profiles = map[string]inventory.Profile{"off": {Export: inventory.ExportNone}}
		}, `profile "off": export none writes nothing`},
		{"route the credential does not open", func(n *inventory.Node) {
			n.Profiles = map[string]inventory.Profile{"browser": {Access: []string{"chain"}}}
		}, `profile "browser": access names route "chain", which this device's credential does not open; it opens sea`},
		{"slash in the name", func(n *inventory.Node) {
			n.Profiles = map[string]inventory.Profile{"a/b": {}}
		}, `profile "a/b": a profile name ends a file name`},
		{"no owner", func(n *inventory.Node) {
			n.Owner = ""
			n.Profiles = map[string]inventory.Profile{"singbox": {}}
		}, `node "laptop": profiles belong to a device`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv := validInventory()
			inv.Nodes[2].Instances = nil
			c.edit(&inv.Nodes[2])
			got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
			if !containsSubstring(got, c.want) {
				t.Fatalf("Validate = %v, want %q", messages(got), c.want)
			}
		})
	}
}

func TestValidate_ProfilesAreClean(t *testing.T) {
	inv := validInventory()
	inv.Nodes[2].Instances = []inventory.Instance{
		{ID: "laptop-sea-ssserver-ss-json-browser", Values: map[string]any{"local_port": 7890}},
	}
	inv.Nodes[2].Profiles = map[string]inventory.Profile{
		"singbox": {Export: "ss-json", Values: map[string]any{"local_port": 2080}},
		"browser": {Access: []string{"sea"}},
	}
	manifests := validManifests()
	if got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil); len(got) != 0 {
		t.Fatalf("Validate = %v, want no issues", messages(got))
	}
}

// TestValidate_PortDispatchFrontsNoName covers rule 18's other half. A relay
// picks its next hop by the port a connection arrived on and matches no name,
// so two ports behind one relay sharing a published name is the ordinary case
// of one machine answering to one name on two numbers — not the ambiguity a
// reverse proxy would have.
func TestValidate_PortDispatchFrontsNoName(t *testing.T) {
	inv := validInventory()
	inv.Nodes[1].Instances[0].Service = "realm"
	inv.Nodes[1].Instances[0].Ports = inventory.PortsOf(map[string]int{"main": 40000, "other": 40001})
	srv := &inv.Nodes[0].Instances[0]
	for _, name := range []string{"main", "alt"} {
		p := srv.Ports[name]
		p.Published = "exit.example.net"
		srv.Ports[name] = p
	}
	manifests := validManifests()
	manifests["realm"] = confgen.Manifest{
		Auth: confgen.AuthNone, Forwards: true, Template: "t",
		Downstreams: confgen.DownstreamsMany, Dispatch: confgen.DispatchPort,
	}
	inv.Routes["chain"] = inventory.Route{Hops: []string{"ss-relay:main", "ss-srv:alt"}}
	inv.Routes["second"] = inventory.Route{Hops: []string{"ss-relay:other", "ss-srv:main"}}

	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, "cannot tell them apart") {
		t.Fatalf("Validate = %v, want no name ambiguity behind a relay", messages(got))
	}
}

// TestValidate_UnknownRuntime is rule 23: `runtime` is one of the three
// words. Nothing else in dgs reads the value, so a misspelling would be
// silent everywhere if this did not report it.
func TestValidate_UnknownRuntime(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Instances[0].Runtime = "dokcer"
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `runtime "dokcer" is not "host", "docker" or "podman"`) {
		t.Fatalf("Validate = %v, want an unknown-runtime issue", messages(got))
	}
}

// TestValidate_KnownRuntimeIsNotAnIssue pins the other side: each of the
// three is accepted, and an instance saying nothing is a host process.
func TestValidate_KnownRuntimeIsNotAnIssue(t *testing.T) {
	for _, runtime := range []string{"", inventory.RuntimeHost, inventory.RuntimeDocker, inventory.RuntimePodman} {
		inv := validInventory()
		inv.Nodes[0].Instances[0].Runtime = runtime
		manifests := validManifests()
		got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
		if len(got) != 0 {
			t.Fatalf("Validate with runtime %q = %v, want none", runtime, messages(got))
		}
	}
}

// TestValidate_DeployWithoutADeployDirectory is rule 24: the values would
// be read by nothing, since the service renders one file.
func TestValidate_DeployWithoutADeployDirectory(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Instances[0].Runtime = inventory.RuntimeDocker
	inv.Nodes[0].Instances[0].Deploy = map[string]any{"image": "example:1"}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `holds no deploy/ directory`) {
		t.Fatalf("Validate = %v, want a missing-deploy-directory issue", messages(got))
	}
}

// TestValidate_DeployOnAHostProcess is the other half of rule 24: a
// deployment file is a container's, and a host process renders none.
func TestValidate_DeployOnAHostProcess(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Instances[0].Deploy = map[string]any{"image": "example:1"}
	manifests := validManifests()
	manifests["ssserver"] = confgen.Manifest{Auth: confgen.AuthPerPrincipal, Exports: []string{"ss-json"}, Template: "t", Deploys: true}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `runs as a host process`) {
		t.Fatalf("Validate = %v, want a host-process issue", messages(got))
	}
}

// TestValidate_DeployWritingPortsOrSecrets is rule 25: both are the second
// spelling the deployment file exists to remove, and the error names the
// key that carries it.
func TestValidate_DeployWritingPortsOrSecrets(t *testing.T) {
	for key, want := range map[string]string{
		"ports":   "a port mapping is derived",
		"secrets": "carries no credential",
	} {
		inv := validInventory()
		inv.Nodes[0].Instances[0].Runtime = inventory.RuntimeDocker
		inv.Nodes[0].Instances[0].Deploy = map[string]any{"image": "example:1", key: "whatever"}
		manifests := validManifests()
		manifests["ssserver"] = confgen.Manifest{Auth: confgen.AuthPerPrincipal, Exports: []string{"ss-json"}, Template: "t", Deploys: true}
		got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
		if !containsSubstring(got, want) {
			t.Fatalf("Validate with deploy %q = %v, want %q", key, messages(got), want)
		}
	}
}

// TestValidate_DeployOnAContainerOfAServiceThatDeploysIsFine keeps the
// rules from reporting the case they exist to allow.
func TestValidate_DeployOnAContainerOfAServiceThatDeploysIsFine(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Instances[0].Runtime = inventory.RuntimeDocker
	inv.Nodes[0].Instances[0].Deploy = map[string]any{"image": "example:1", "restart": "unless-stopped"}
	manifests := validManifests()
	manifests["ssserver"] = confgen.Manifest{Auth: confgen.AuthPerPrincipal, Exports: []string{"ss-json"}, Template: "t", Deploys: true}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if len(got) != 0 {
		t.Fatalf("Validate = %v, want none", messages(got))
	}
}

func principalInventory() (*inventory.Root, map[string]confgen.Manifest) {
	inv := validInventory()
	inv.Nodes[1].Instances[0].Principal = "repeater"
	inv.Users["repeater"] = inventory.User{Devices: inventory.DevicesNone, Access: []string{"chain"}}
	manifests := validManifests()
	manifests["ssserver"] = confgen.Manifest{
		Auth: confgen.AuthPerPrincipal, Exports: []string{"ss-json"}, Template: "t",
		Upstream: confgen.UpstreamDecls{confgen.UpstreamValues: {}},
	}
	return inv, manifests
}

func TestValidate_PrincipalIsKnownAuthorisedAndConsumed(t *testing.T) {
	inv, manifests := principalInventory()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if len(got) != 0 {
		t.Fatalf("Validate = %v, want none", messages(got))
	}
}

func TestValidate_PrincipalMustNameAUser(t *testing.T) {
	inv, manifests := principalInventory()
	inv.Nodes[1].Instances[0].Principal = "nobody"
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `instance "ss-relay": principal "nobody" is not a user`) {
		t.Fatalf("Validate = %v, want unknown principal", messages(got))
	}
}

func TestValidate_PrincipalMustHoldTheRoute(t *testing.T) {
	inv, manifests := principalInventory()
	inv.Users["repeater"] = inventory.User{Devices: inventory.DevicesNone}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `instance "ss-relay": principal "repeater"'s "default" credential has no access to route "chain"`) {
		t.Fatalf("Validate = %v, want unauthorised principal", messages(got))
	}
}

// The credential an instance carries is the principal's `default`, so a person
// holding the route under another credential is not the question: what matters
// is whether that one credential opens it.
func TestValidate_PrincipalDefaultCredentialMustNotNarrowAwayTheRoute(t *testing.T) {
	inv, manifests := principalInventory()
	inv.Users["repeater"] = inventory.User{
		Devices: inventory.DevicesNone,
		Access:  []string{"chain", "sea"},
		Credentials: map[string]inventory.Credential{
			"default": {Access: []string{"sea"}},
		},
	}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `instance "ss-relay": principal "repeater"'s "default" credential has no access to route "chain"`) {
		t.Fatalf("Validate = %v, want the narrowed default credential reported", messages(got))
	}
}

// A person who declares credentials and keeps no `default` has nothing for the
// instance to carry, however wide their own access is.
func TestValidate_PrincipalMustKeepADefaultCredential(t *testing.T) {
	inv, manifests := principalInventory()
	inv.Users["repeater"] = inventory.User{
		Devices:     inventory.DevicesNone,
		Access:      []string{"chain"},
		Credentials: map[string]inventory.Credential{"relay": {}},
	}
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `instance "ss-relay": principal "repeater"'s "default" credential has no access to route "chain"`) {
		t.Fatalf("Validate = %v, want the missing default credential reported", messages(got))
	}
}

func TestValidate_PrincipalNeedsAnUpstreamConsumer(t *testing.T) {
	inv, manifests := principalInventory()
	manifest := manifests["ssserver"]
	manifest.Upstream = nil
	manifests["ssserver"] = manifest
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `instance "ss-relay": principal "repeater" is unused because service "ssserver" declares no upstream`) {
		t.Fatalf("Validate = %v, want unused principal", messages(got))
	}
}

// TestValidate_TwoCredentialsOnAPersonNamedPort is what naming accounts by
// person trades away: two of one person's credentials on the port render two
// accounts under one name. Rule 13 reports it here, where the grant is
// written, rather than leaving an account table with a line that silently
// overwrites the one before it.
func TestValidate_TwoCredentialsOnAPersonNamedPort(t *testing.T) {
	inv := &inventory.Root{
		Nodes: []inventory.Node{{
			ID:       "nas",
			Networks: inventory.Networks{"internet": "nas.example.net"},
			Instances: []inventory.Instance{
				{ID: "samba-nas", Service: "samba", Ports: inventory.PortsOf(map[string]int{"smb": 445})},
			},
		}},
		Users: map[string]inventory.User{
			"erin": {
				Devices: inventory.DevicesNone,
				Access:  []string{"files"},
				Credentials: map[string]inventory.Credential{
					"default": {},
					"laptop":  {},
				},
			},
		},
		Routes:    map[string]inventory.Route{"files": {Hops: []string{"samba-nas:smb"}}},
		Networks:  []string{"internet"},
		Universal: "internet",
	}
	manifests := map[string]confgen.Manifest{
		"samba": {Auth: confgen.AuthPerPrincipal, Accounts: confgen.AccountsPerson, Template: "t", Output: "smb.conf"},
	}
	got := Validate(inv, manifests, nil, derived(t, inv, manifests), nil)
	if !containsSubstring(got, `account "erin" is rendered by more than one principal`) {
		t.Fatalf("issues = %v, want rule 13 to report erin's two credentials", messages(got))
	}
}

// TestValidate_ContainerNetworks: rule 32 on a node's container networks and
// the addresses fixed on them.
func TestValidate_ContainerNetworks(t *testing.T) {
	inv := validInventory()
	srv := &inv.Nodes[0]
	srv.Runtime = inventory.RuntimeDocker
	srv.Containers = []inventory.ContainerNetwork{
		{Name: "apps", Subnet: "172.29.0.0/24"},
		{Name: "tailnet", Subnet: "172.29.250.0/24", Gateway: "172.29.250.1"},
		{Name: "wide", Subnet: "172.29.0.0/16"},
		{Name: "loose", Subnet: "not-a-prefix"},
	}
	srv.Instances[0].Runtime = inventory.RuntimeDocker
	srv.Instances[0].Containers = map[string]string{"tailnet": "172.29.250.1", "apps": "10.0.0.5", "gone": ""}
	srv.Instances = append(srv.Instances,
		inventory.Instance{ID: "one", Service: "ssserver", Runtime: inventory.RuntimeDocker, Containers: map[string]string{"tailnet": "172.29.250.10"}},
		inventory.Instance{ID: "two", Service: "ssserver", Runtime: inventory.RuntimeDocker, Containers: map[string]string{"tailnet": "172.29.250.10"}},
		inventory.Instance{ID: "host", Service: "ssserver", Runtime: inventory.RuntimeHost, Containers: map[string]string{"apps": ""}},
	)
	got := Validate(inv, validManifests(), validExports(), &derive.Model{}, nil)
	for _, want := range []string{
		`container networks "apps" and "wide" overlap`,
		`container network "loose": subnet "not-a-prefix" is not a CIDR prefix`,
		`address 172.29.250.1 on container network "tailnet" is the network's gateway`,
		`address 10.0.0.5 on container network "apps" is outside its subnet 172.29.0.0/24`,
		`joins container network "gone", which node "srv" does not list`,
		`instances "one" and "two" both hold 172.29.250.10 on container network "tailnet"`,
		`instance "host" runs as a host process and names container networks apps`,
	} {
		if !containsSubstring(got, want) {
			t.Errorf("Validate = %v, want %q", messages(got), want)
		}
	}
}

// containerScopeInventory puts ss-srv in a container on srv's bridge br and
// adds a route scoped to that bridge.
func containerScopeInventory() *inventory.Root {
	inv := validInventory()
	inv.Nodes[0].Containers = []inventory.ContainerNetwork{{Name: "br", Subnet: "172.29.250.0/24"}}
	inv.Nodes[0].Instances[0].Runtime = inventory.RuntimeDocker
	inv.Nodes[0].Instances[0].Containers = map[string]string{"br": "172.29.250.53"}
	inv.Routes["br/dns"] = inventory.Route{Hops: []string{"ss-srv:alt"}, Scope: "br"}
	return inv
}

func TestValidate_RouteScopedToAContainerNetworkItsEntryJoins(t *testing.T) {
	inv := containerScopeInventory()
	manifests := validManifests()
	for _, iss := range Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil) {
		if strings.Contains(iss.Message, "br") {
			t.Fatalf("Validate = %v, want no issue about the br scope", iss.Message)
		}
	}
}

func TestValidate_RouteScopedToAContainerNetworkItsEntryDoesNotJoin(t *testing.T) {
	inv := containerScopeInventory()
	inv.Nodes[0].Containers = append(inv.Nodes[0].Containers, inventory.ContainerNetwork{Name: "apps", Subnet: "172.29.0.0/24"})
	inv.Nodes[0].Instances[0].Containers = map[string]string{"apps": ""}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `route "br/dns" is scoped to container network "br", and its entry ss-srv:alt does not join it`) {
		t.Fatalf("Validate = %v, want an entry-not-joined issue", messages(got))
	}
}

func TestValidate_CredentialOpeningAContainerScopedRoute(t *testing.T) {
	inv := containerScopeInventory()
	inv.Users["dana"] = inventory.User{Username: "dana", Access: []string{"sea", "br/dns"}}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `opens route "br/dns", scoped to a container network`) {
		t.Fatalf("Validate = %v, want a container-scope credential issue", messages(got))
	}
}

func TestValidate_ContainerNetworkNamedLikeANetwork(t *testing.T) {
	inv := validInventory()
	inv.Nodes[0].Containers = []inventory.ContainerNetwork{{Name: "home", Subnet: "172.29.250.0/24"}}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, `node "srv": container network "home" has the name of a network or a node`) {
		t.Fatalf("Validate = %v, want an ambiguous-name issue", messages(got))
	}
}

// TestValidate_ContainersOnOnePortPublishedApart: two containers listening on
// one port number in their own network namespaces collide only where the
// host mappings do. ss-srv:alt is published on the node's address for the
// relay; ss-srv2:alt, scoped to the bridge, is published nowhere.
func TestValidate_ContainersOnOnePortPublishedApart(t *testing.T) {
	inv := containerScopeInventory()
	inv.Nodes[0].Instances[0].Bind = "0.0.0.0"
	inv.Nodes[0].Instances = append(inv.Nodes[0].Instances, inventory.Instance{
		ID: "ss-srv2", Service: "ssserver", Runtime: inventory.RuntimeDocker, Bind: "0.0.0.0",
		Containers: map[string]string{"br": ""},
		Ports:      inventory.PortsOf(map[string]int{"alt": 49217}),
	})
	inv.Routes["br/dns"] = inventory.Route{Hops: []string{"ss-srv2:alt"}, Scope: "br"}
	manifests := validManifests()
	got := Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if containsSubstring(got, "bind the same address") {
		t.Fatalf("Validate = %v, want no bind collision: ss-srv2:alt publishes nowhere", messages(got))
	}

	// Without the scope ss-srv2:alt is reached from outside, published on
	// the node's address like ss-srv:alt, and the two collide.
	delete(inv.Routes, "br/dns")
	got = Validate(inv, manifests, validExports(), derived(t, inv, manifests), nil)
	if !containsSubstring(got, "ss-srv2:alt, ss-srv:alt bind the same address, port and protocol (203.0.113.10:49217/tcp)") {
		t.Fatalf("Validate = %v, want the two published on one host address to collide", messages(got))
	}
}
