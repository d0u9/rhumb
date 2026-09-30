package cli

// Settings is where a command finds a generator root and writes: the
// generator root, the directory its secrets are stored in, and the default
// export destination. A caller fills it from flags or its own configuration.
type Settings struct {
	Root      string
	Secrets   string
	ExportDir string
}
