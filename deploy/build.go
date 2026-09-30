package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

// Options are the choices Build leaves to its caller.
type Options struct {
	// Platform is the target's GOOS/GOARCH, this machine's when empty.
	Platform string
	// Services is a directory of service definitions consulted before the
	// built-in ones.
	Services string
	// Binary is a local program to bundle instead of what the source
	// would give, for a machine with no network or a build of one's own.
	Binary string
}

// Build writes the bundle for the exported instance in src into dst.
//
// A bundle is self-contained and does not know where it will be installed:
// ctl finds its own directory when it runs. Rebuilding into an existing
// bundle replaces ctl, bin/, conf/ and manifest.yaml and keeps var/, where
// the running program's state and logs are.
func Build(src, dst string, opt Options) error {
	m, err := ReadManifest(src)
	if err != nil {
		return err
	}
	if m.Container != nil {
		return buildDocker(m, src, dst)
	}
	if m.Runtime != "host" {
		return fmt.Errorf("%s/%s runs in %s and its manifest has no container: its service holds no docker.yaml", m.Node, m.Instance, m.Runtime)
	}
	platform := opt.Platform
	if platform == "" {
		platform = m.Platform
	}
	if platform == "" {
		platform = runtime.GOOS + "/" + runtime.GOARCH
	}
	tmpl, ok := ctlTemplates[strings.SplitN(platform, "/", 2)[0]]
	if !ok {
		return fmt.Errorf("platform %s: only darwin (launchd) and linux (systemd) bundles are supported", platform)
	}
	svc, err := LoadService(m.Service, opt.Services)
	if err != nil {
		return err
	}
	if len(svc.EnvFiles) > 0 && strings.HasPrefix(platform, "darwin/") {
		return fmt.Errorf("service %q: env_files is not supported by launchd bundles yet", m.Service)
	}
	label := Label(m)
	if old, err := os.ReadFile(filepath.Join(dst, "ctl")); err == nil && !bytes.Contains(old, []byte("LABEL="+label+"\n")) {
		return fmt.Errorf("%s holds another bundle; not replacing it", dst)
	}

	for _, d := range []string{"bin", "conf"} {
		if err := os.RemoveAll(filepath.Join(dst, d)); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(dst, "conf"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dst, "bin"), 0o755); err != nil {
		return err
	}
	for _, f := range m.Files {
		mode := os.FileMode(0o600)
		if f.Executable {
			mode = 0o700
		}
		if err := copyFile(filepath.Join(src, f.Path), filepath.Join(dst, "conf", f.Path), mode); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(src, ManifestFile), filepath.Join(dst, ManifestFile), 0o600); err != nil {
		return err
	}
	source := svc.Source()
	bin := filepath.Join(dst, "bin", svc.Binary.Name)
	switch {
	case opt.Binary != "":
		source = SourceRelease // carried in the bundle like a release
		err = copyFile(opt.Binary, bin, 0o755)
	case source == SourceRelease:
		err = fetch(svc, platform, bin)
	}
	if err != nil {
		return err
	}
	ctl, err := renderCtl(tmpl, m, svc, label, source)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "ctl"), ctl, 0o755)
}

// Label is the name the service manager knows an instance by. The prefix is
// what gc recognises as rhumb's.
func Label(m Manifest) string {
	return "rhumb." + m.Node + "." + m.Instance
}

func copyFile(from, to string, mode os.FileMode) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	return os.WriteFile(to, data, mode)
}

// fetch downloads the service's release for platform, once per machine, and
// unpacks its binary to bin.
func fetch(svc Service, platform, bin string) error {
	rel, err := svc.Binary.Release.Resolve()
	if err != nil {
		return err
	}
	url, err := rel.URLFor(platform)
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	archive := filepath.Join(cache, "rhumb", "downloads", filepath.Base(url))
	if _, err := os.Stat(archive); errors.Is(err, os.ErrNotExist) {
		if err := download(url, archive); err != nil {
			return err
		}
	}
	tmp, err := os.MkdirTemp("", "rhumb-unpack-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	// tar reads every format a release comes in, xz included, without this
	// program carrying a decompressor for each.
	if out, err := exec.Command("tar", "-xf", archive, "-C", tmp, svc.Binary.Name).CombinedOutput(); err != nil {
		return fmt.Errorf("unpacking %s: %v: %s", filepath.Base(archive), err, out)
	}
	return copyFile(filepath.Join(tmp, svc.Binary.Name), bin, 0o755)
}

func download(url, to string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	part := to + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, to)
}

// shellArg quotes a command argument for sh, turning {bin} and {conf} into
// the installed bundle's directories.
func shellArg(arg string) string {
	q := "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	q = strings.ReplaceAll(q, "{bin}", `'"$BIN"'`)
	q = strings.ReplaceAll(q, "{conf}", `'"$DIR/conf"'`)
	// A placeholder at either end leaves an empty '' there, which quotes nothing.
	return strings.TrimSuffix(strings.TrimPrefix(q, "''"), "''")
}

// ctlTemplates is the ctl of a host bundle by GOOS: each registers the
// program with that system's service manager.
var ctlTemplates = map[string]*template.Template{
	"darwin": template.Must(template.New("ctl").Parse(ctlDarwin)),
	"linux":  template.Must(template.New("ctl").Parse(ctlLinux)),
}

func renderCtl(tmpl *template.Template, m Manifest, svc Service, label, source string) ([]byte, error) {
	args := make([]string, len(svc.Command))
	for i, a := range svc.Command {
		args[i] = shellArg(a)
	}
	var out bytes.Buffer
	envFiles := make([]string, len(svc.EnvFiles))
	for i, f := range svc.EnvFiles {
		envFiles[i] = shellArg("{conf}/" + f)
	}
	err := tmpl.Execute(&out, map[string]any{
		"M": m, "Label": label, "Args": strings.Join(args, " "), "Expose": strings.Join(svc.Expose, " "),
		"EnvFiles": strings.Join(envFiles, " "), "Source": source,
		"BinDir": binDir(svc, source), "InstallBinary": installBinary(svc, source),
	})
	return out.Bytes(), err
}

// binDir is the shell expression ctl sets BIN to: the directory the
// program is in on the machine.
func binDir(svc Service, source string) string {
	switch source {
	case SourcePath:
		return shQuote(path.Dir(svc.Binary.Path))
	case SourceApt:
		return `$(dirname "$(command -v ` + shQuote(svc.Binary.Name) + ` || echo /nonexistent/x)")`
	}
	return `"$DIR/bin"`
}

// installBinary is the body of ctl's install_binary: what puts the program
// on the machine when the bundle does not carry it.
func installBinary(svc Service, source string) string {
	name := shQuote(svc.Binary.Name)
	src := svc.Binary
	switch source {
	case SourcePath:
		return fmt.Sprintf("[ -x %s ] || { echo \"%s is not on this machine\" >&2; exit 1; }", shQuote(src.Path), strings.ReplaceAll(src.Path, `"`, ""))
	case SourceApt:
		return fmt.Sprintf("command -v %s >/dev/null 2>&1 || { apt-get update && apt-get install -y %s; }\n\tBIN=%s", name, shQuote(src.Apt), binDir(svc, source))
	}
	return ":"
}
