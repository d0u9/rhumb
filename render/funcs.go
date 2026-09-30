package render

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/d0u9/rhumb/smbpasswd"
)

// funcs builds the template.FuncMap: the function set
// docs/export.md#the-template-language names, closing over
// defaults and the render context in's other fields carry.
func funcs(defaults map[string]any, in Input) map[string]any {
	return map[string]any{
		"merge":    mergeFunc,
		"omit":     omitFunc,
		"pick":     pickFunc,
		"has":      hasFunc,
		"append":   appendFunc,
		"slice":    sliceFunc,
		"dict":     dictFunc,
		"toYAML":   toYAMLFunc,
		"toJSON":   toJSONFunc,
		"required": requiredFunc,
		"b64":      b64Func,
		"join":     joinFunc,
		// Samba stores the NT hash of a password rather than the password,
		// so an account table can be rendered without putting a plaintext
		// credential in a deployed file. The line's layout is positional
		// and unforgiving, which is why smbpasswd writes the whole entry
		// rather than leaving a template to place six colons correctly.
		"nthash":    smbpasswd.NTHash,
		"smbpasswd": smbpasswdFunc,
		"secret":    secretFunc(in.Self),
		"defaults":  func() map[string]any { return defaults },
		"node":      func() map[string]any { return in.Node },
		"instance":  func() map[string]any { return in.Instance },
		"upstream": func() (map[string]any, error) {
			if in.Upstream == nil && len(in.Upstreams) > 1 {
				routes := make([]string, len(in.Upstreams))
				for i, u := range in.Upstreams {
					routes[i], _ = u["route"].(string)
				}
				return nil, fmt.Errorf("this program takes one upstream, and it is given %d routes (%s): narrow the profile's access, or the export's routes, to one", len(routes), strings.Join(routes, ", "))
			}
			return in.Upstream, nil
		},
		"upstreams": func() []map[string]any { return in.Upstreams },
		"replace":   func(s, old, new string) string { return strings.ReplaceAll(s, old, new) },
		"principals": func(port string) []Principal {
			return in.Principals[port]
		},
		"grantees": func(port string) []Grantee {
			return grantees(in.Principals[port])
		},
		"downstreams": func() []Downstream { return in.Downstreams },
		"published":   func(port string) string { return in.Published[port] },
		"publishedNames": func(port string) []string {
			return in.PublishedNames[port]
		},
		"mapping": func(port string) Mapping { return in.Mapping[port] },
		"dial": func(name string) (Downstream, error) {
			d, ok := in.Dials[name]
			if !ok {
				return Downstream{}, fmt.Errorf("dial %q: the instance declares no such dial", name)
			}
			return d, nil
		},
		"names": func(network string) []Name { return in.Names[network] },
		"target": func() map[string]string {
			return map[string]string{
				"service":  in.Target.Service,
				"instance": in.Target.Instance,
			}
		},
	}
}

// mergeFunc implements merge: the first argument wins, each later one losing
// to every earlier one. Nested maps merge key by key; a list loses wholesale
// to an earlier list rather than being merged into it — see
// docs/export.md#the-template-language.
func mergeFunc(args ...any) (map[string]any, error) {
	acc := map[string]any{}
	for i := len(args) - 1; i >= 0; i-- {
		m, ok := args[i].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("merge: argument %d is not a map, got %T", i, args[i])
		}
		acc = mergeInto(m, acc)
	}
	return acc, nil
}

// mergeInto merges src under dst, dst winning: a key present in both, with
// both values maps, merges recursively; otherwise dst's value stands.
func mergeInto(dst, src map[string]any) map[string]any {
	result := make(map[string]any, len(src)+len(dst))
	for k, v := range src {
		result[k] = v
	}
	for k, v := range dst {
		if existing, ok := result[k]; ok {
			if vMap, ok1 := v.(map[string]any); ok1 {
				if exMap, ok2 := existing.(map[string]any); ok2 {
					result[k] = mergeInto(vMap, exMap)
					continue
				}
			}
		}
		result[k] = v
	}
	return result
}

func omitFunc(m map[string]any, keys ...string) map[string]any {
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		if !drop[k] {
			result[k] = v
		}
	}
	return result
}

func pickFunc(m map[string]any, keys ...string) map[string]any {
	result := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			result[k] = v
		}
	}
	return result
}

// hasFunc reports whether a map has a key, or a list holds a value.
func hasFunc(collection any, item any) bool {
	switch c := collection.(type) {
	case map[string]any:
		key, ok := item.(string)
		if !ok {
			return false
		}
		_, exists := c[key]
		return exists
	case []any:
		for _, v := range c {
			if reflect.DeepEqual(v, item) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// appendFunc returns list with items added, without modifying list.
func appendFunc(list []any, items ...any) []any {
	result := make([]any, 0, len(list)+len(items))
	result = append(result, list...)
	result = append(result, items...)
	return result
}

func sliceFunc(items ...any) []any {
	return append([]any{}, items...)
}

// dictFunc builds a map from alternating key, value arguments. Keys must be
// strings.
func dictFunc(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments (%d)", len(pairs))
	}
	result := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %d is not a string, got %T", i/2, pairs[i])
		}
		result[key] = pairs[i+1]
	}
	return result, nil
}

func toYAMLFunc(v any) (string, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("toYAML: %w", err)
	}
	return string(out), nil
}

func toJSONFunc(v any) (string, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("toJSON: %w", err)
	}
	return string(out), nil
}

// requiredFunc returns value unchanged unless it is nil or the zero value of
// a string, map or slice, in which case it errors naming what was missing.
func requiredFunc(value any, name string) (any, error) {
	if value == nil {
		return nil, fmt.Errorf("required: %s is missing", name)
	}
	switch v := reflect.ValueOf(value); v.Kind() {
	case reflect.String:
		if v.Len() == 0 {
			return nil, fmt.Errorf("required: %s is missing", name)
		}
	case reflect.Map, reflect.Slice:
		if v.Len() == 0 {
			return nil, fmt.Errorf("required: %s is missing", name)
		}
	}
	return value, nil
}

// secretFunc returns one of the instance's own secrets — a TLS key, an
// administrative password, one PSK of several — from the render context's
// self datasource. Further arguments narrow it: a key for a `set` name, a
// field for a name with fields, both for a name with both. Given fewer
// arguments than the name has levels, it returns the map of what is under
// it, so a template can range over a set. A name or key matching nothing is
// an error naming it, rather than an empty value that fails further down.
// See docs/export.md#the-template-language.
func secretFunc(self map[string]any) func(name string, more ...string) (any, error) {
	return func(name string, more ...string) (any, error) {
		at, ok := self[name]
		if !ok {
			return nil, fmt.Errorf("secret: no own secret named %q", name)
		}
		walked := []string{name}
		for _, segment := range more {
			m, ok := at.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("secret: %s is one value and takes no %q", strings.Join(walked, "."), segment)
			}
			at, ok = m[segment]
			if !ok {
				return nil, fmt.Errorf("secret: %s has nothing named %q", strings.Join(walked, "."), segment)
			}
			walked = append(walked, segment)
		}
		return at, nil
	}
}

// joinFunc concatenates values with a separator, which is how the protocols
// that take more than one credential take them: a Shadowsocks 2022 password
// is the server's PSK and the user's own, joined by a colon, and a relay
// chain is longer still.
func joinFunc(sep string, values any) (string, error) {
	switch v := values.(type) {
	case []string:
		return strings.Join(v, sep), nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("join: want strings, got %T", item)
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, sep), nil
	case string:
		return v, nil
	case nil:
		return "", nil
	}
	return "", fmt.Errorf("join: want a list of strings, got %T", values)
}

// b64Func encodes a string the way a share URI carries credentials:
// base64url without padding, which is what SIP002's `ss://` userinfo is and
// what every client that reads one expects. Standard base64 with padding
// would need percent-escaping in a URL, and the unpadded URL alphabet needs
// neither.
func b64Func(value any) (string, error) {
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("b64: want a string, got %T", value)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(s)), nil
}

// smbpasswdFunc implements smbpasswd: one line of a Samba account table for
// a principal, holding the NT hash of their secret and never the secret.
// The uid is the account's where Samba runs — an entry whose uid belongs to
// no POSIX account is one smbd refuses to authenticate — and it comes from
// the instance, since which numbers a machine's accounts have is that
// machine's fact and not this service's.
func smbpasswdFunc(name string, uid any, password string) (string, error) {
	n, err := toInt(uid)
	if err != nil {
		return "", fmt.Errorf("smbpasswd %q: uid: %w", name, err)
	}
	return smbpasswd.Entry(name, n, password), nil
}

// toInt reads a uid from a template argument. YAML gives an int, a template
// literal may give any numeric kind, and a values file quoting the number
// gives a string: all three name one uid, and refusing two of them would be
// an error about how the number was written rather than about what it says.
func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("%v is not a whole number", v)
		}
		return int(n), nil
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("%q is not a number", n)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("%v is not a number, got %T", v, v)
	}
}

// grantees groups a port's principals by the person holding them, in the
// order that person's first credential appears, so a rendered file does not
// reorder because a credential was added.
func grantees(principals []Principal) []Grantee {
	var out []Grantee
	at := map[string]int{}
	for _, p := range principals {
		if p.User == "" {
			continue
		}
		i, ok := at[p.User]
		if !ok {
			at[p.User] = len(out)
			out = append(out, Grantee{User: p.User, Accounts: []string{p.Name}})
			continue
		}
		out[i].Accounts = append(out[i].Accounts, p.Name)
	}
	return out
}
