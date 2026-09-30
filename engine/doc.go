// Package engine loads a generator root and renders its targets: every
// instance's configuration files, the deployment files beside a
// containerised one, and the manifest a deployment tool reads. It writes
// nothing to disk; a caller publishes what RenderAll returns.
package engine
