package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Mathias-g/Servitor/internal/components/secret"
	"github.com/Mathias-g/Servitor/internal/integrations"
)

// These commands manage the declared integrations config (ADR-0018): the local
// file that declares which subprocess integrations (MCP servers, Singer taps
// and targets) are available. They edit the config directly; the actual
// software install is delegated to the ecosystem's package managers, which the
// operator runs separately. There is no daemon round-trip: the config is an
// operator-side file the CLI and `capabilities` both read.

// integrationsPath returns the config path from the --file flag, or the
// default.
func integrationsPath(fs *flag.FlagSet, args []string) (string, []string, int) {
	path := integrations.DefaultFile
	fs.StringVar(&path, "file", integrations.DefaultFile, "path to the integrations config")
	if err := fs.Parse(args); err != nil {
		return "", nil, exitUsage
	}
	return path, fs.Args(), exitOK
}

// cmdMCP manages declared MCP servers.
func cmdMCP(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor mcp <add|list|remove> ...\n")
		return exitUsage
	}
	switch args[0] {
	case "add":
		return cmdMCPAdd(args[1:], stdout, stderr)
	case "list", "ls":
		return cmdMCPList(args[1:], stdout, stderr)
	case "remove", "rm":
		return cmdMCPRemove(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "servitor: mcp: unknown subcommand %q\n", args[0])
		return exitUsage
	}
}

func cmdMCPAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) < 2 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor mcp add <name> <command> [env,...]\n")
		return exitUsage
	}
	name := rest[0]
	command := strings.Fields(rest[1])
	var env []string
	if len(rest) > 2 {
		env = splitList(rest[2])
	}

	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: mcp add: %v\n", err)
		return exitFailure
	}
	cfg.AddMCPServer(name, command, env)
	if err := integrations.Save(cfg, path); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: mcp add: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: declared mcp server %q (install it yourself, then it is ready)\n", name)
	return exitOK
}

func cmdMCPList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: mcp list: unexpected argument %q\n", rest[0])
		return exitUsage
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: mcp list: %v\n", err)
		return exitFailure
	}
	for _, name := range cfg.ServerNames() {
		s := cfg.MCP[name]
		_, _ = fmt.Fprintf(stdout, "%s\t%s\n", name, strings.Join(s.Command, " "))
	}
	return exitOK
}

func cmdMCPRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor mcp remove <name>\n")
		return exitUsage
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: mcp remove: %v\n", err)
		return exitFailure
	}
	if !cfg.RemoveMCPServer(rest[0]) {
		_, _ = fmt.Fprintf(stderr, "servitor: mcp remove: %q is not declared\n", rest[0])
		return exitFailure
	}
	if err := integrations.Save(cfg, path); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: mcp remove: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: removed mcp server %q\n", rest[0])
	return exitOK
}

// cmdTap manages declared Singer taps.
func cmdTap(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor tap <add|list|remove> ...\n")
		return exitUsage
	}
	switch args[0] {
	case "add":
		return cmdTapAdd(args[1:], stdout, stderr)
	case "list", "ls":
		return cmdTapList(args[1:], stdout, stderr)
	case "remove", "rm":
		return cmdTapRemove(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "servitor: tap: unknown subcommand %q\n", args[0])
		return exitUsage
	}
}

func cmdTapAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tap add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) < 2 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor tap add <name> <command> [env,...]\n")
		return exitUsage
	}
	name := rest[0]
	command := strings.Fields(rest[1])
	var env []string
	if len(rest) > 2 {
		env = splitList(rest[2])
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: tap add: %v\n", err)
		return exitFailure
	}
	cfg.AddTap(name, command, env)
	if err := integrations.Save(cfg, path); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: tap add: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: declared tap %q (install it yourself, then it is ready)\n", name)
	return exitOK
}

func cmdTapList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tap list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: tap list: unexpected argument %q\n", rest[0])
		return exitUsage
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: tap list: %v\n", err)
		return exitFailure
	}
	for _, name := range cfg.TapNames() {
		t := cfg.Singer.Taps[name]
		_, _ = fmt.Fprintf(stdout, "%s\t%s\n", name, strings.Join(t.Command, " "))
	}
	return exitOK
}

func cmdTapRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tap remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor tap remove <name>\n")
		return exitUsage
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: tap remove: %v\n", err)
		return exitFailure
	}
	if !cfg.RemoveTap(rest[0]) {
		_, _ = fmt.Fprintf(stderr, "servitor: tap remove: %q is not declared\n", rest[0])
		return exitFailure
	}
	if err := integrations.Save(cfg, path); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: tap remove: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: removed tap %q\n", rest[0])
	return exitOK
}

// splitList splits a comma-separated list of env var names.
func splitList(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// cmdTarget manages declared Singer targets.
func cmdTarget(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor target <add|list|remove> ...\n")
		return exitUsage
	}
	switch args[0] {
	case "add":
		return cmdTargetAdd(args[1:], stdout, stderr)
	case "list", "ls":
		return cmdTargetList(args[1:], stdout, stderr)
	case "remove", "rm":
		return cmdTargetRemove(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "servitor: target: unknown subcommand %q\n", args[0])
		return exitUsage
	}
}

func cmdTargetAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("target add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) < 2 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor target add <name> <command> [env,...]\n")
		return exitUsage
	}
	name := rest[0]
	command := strings.Fields(rest[1])
	var env []string
	if len(rest) > 2 {
		env = splitList(rest[2])
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: target add: %v\n", err)
		return exitFailure
	}
	cfg.AddTarget(name, command, env)
	if err := integrations.Save(cfg, path); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: target add: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: declared target %q (install it yourself, then it is ready)\n", name)
	return exitOK
}

func cmdTargetList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("target list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: target list: unexpected argument %q\n", rest[0])
		return exitUsage
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: target list: %v\n", err)
		return exitFailure
	}
	for _, name := range cfg.TargetNames() {
		t := cfg.Singer.Targets[name]
		_, _ = fmt.Fprintf(stdout, "%s\t%s\n", name, strings.Join(t.Command, " "))
	}
	return exitOK
}

func cmdTargetRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("target remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor target remove <name>\n")
		return exitUsage
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: target remove: %v\n", err)
		return exitFailure
	}
	if !cfg.RemoveTarget(rest[0]) {
		_, _ = fmt.Fprintf(stderr, "servitor: target remove: %q is not declared\n", rest[0])
		return exitFailure
	}
	if err := integrations.Save(cfg, path); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: target remove: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: removed target %q\n", rest[0])
	return exitOK
}

// cmdSecret manages the declared secrets section of the integrations config
// (ADR-0035). A secret entry carries a name (the map key), a required source,
// and optional account/permissions/expiry metadata. Values are never stored or
// shown here; the operator/CI delivers the value to the named source.
func cmdSecret(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor secret <add|list|remove|seal> ...\n")
		return exitUsage
	}
	switch args[0] {
	case "add":
		return cmdSecretAdd(args[1:], stdout, stderr)
	case "list", "ls":
		return cmdSecretList(args[1:], stdout, stderr)
	case "remove", "rm":
		return cmdSecretRemove(args[1:], stdout, stderr)
	case "seal":
		return cmdSecretSeal(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "servitor: secret: unknown subcommand %q\n", args[0])
		return exitUsage
	}
}

// cmdSecretSeal seals a secret value into the on-box ciphertext store, reading
// the value from stdin so it never appears in argv or shell history (the push
// path for the recommended onbox mechanism, SPEC: Secret resolution). It is
// meant to be run on the box by CI/CD or the operator.
func cmdSecretSeal(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor secret seal <name> < value-on-stdin\n")
		return exitUsage
	}
	data, err := io.ReadAll(stdinReader())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: secret seal: read value: %v\n", err)
		return exitFailure
	}
	if len(data) == 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: secret seal: empty value from stdin\n")
		return exitFailure
	}
	if err := secret.SealOnBox("", args[0], string(data)); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: secret seal: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: sealed secret %q to the on-box store (never plaintext on disk)\n", args[0])
	return exitOK
}

// stdinReader returns the reader the CLI should read a value from (os.Stdin).
var stdinReader = func() io.Reader { return os.Stdin }

func cmdSecretAdd(args []string, stdout, stderr io.Writer) int {
	path := integrations.DefaultFile
	account, permissions, expiry := "", "", ""
	var positionals []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--file":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintf(stderr, "servitor: secret add: --file needs a value\n")
				return exitUsage
			}
			i++
			path = args[i]
		case "--account":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintf(stderr, "servitor: secret add: --account needs a value\n")
				return exitUsage
			}
			i++
			account = args[i]
		case "--permissions":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintf(stderr, "servitor: secret add: --permissions needs a value\n")
				return exitUsage
			}
			i++
			permissions = args[i]
		case "--expiry":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintf(stderr, "servitor: secret add: --expiry needs a value\n")
				return exitUsage
			}
			i++
			expiry = args[i]
		case "-h", "--help":
			_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor secret add <name> <source> [--account a] [--permissions p,...] [--expiry e] [--file path]\n")
			return exitUsage
		default:
			positionals = append(positionals, args[i])
		}
	}
	if len(positionals) != 2 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor secret add <name> <source> [--account a] [--permissions p,...] [--expiry e] [--file path]\n")
		return exitUsage
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: secret add: %v\n", err)
		return exitFailure
	}
	cfg.AddSecret(positionals[0], &integrations.Secret{
		Source:      positionals[1],
		Account:     account,
		Permissions: splitList(permissions),
		Expiry:      expiry,
	})
	if err := integrations.Save(cfg, path); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: secret add: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: declared secret %q (source %q); deliver its value to that source\n", positionals[0], positionals[1])
	return exitOK
}

func cmdSecretList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("secret list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) > 0 {
		_, _ = fmt.Fprintf(stderr, "servitor: secret list: unexpected argument %q\n", rest[0])
		return exitUsage
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: secret list: %v\n", err)
		return exitFailure
	}
	names := make([]string, 0, len(cfg.Secrets))
	for name := range cfg.Secrets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := cfg.Secrets[name]
		_, _ = fmt.Fprintf(stdout, "%s\tsource=%s", name, s.Source)
		if s.Account != "" {
			_, _ = fmt.Fprintf(stdout, "\taccount=%s", s.Account)
		}
		if len(s.Permissions) > 0 {
			_, _ = fmt.Fprintf(stdout, "\tpermissions=%s", strings.Join(s.Permissions, ","))
		}
		if s.Expiry != "" {
			_, _ = fmt.Fprintf(stdout, "\texpiry=%s", s.Expiry)
		}
		_, _ = fmt.Fprintln(stdout)
	}
	return exitOK
}

func cmdSecretRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("secret remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path, rest, code := integrationsPath(fs, args)
	if code != exitOK {
		return code
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "servitor: usage: servitor secret remove <name>\n")
		return exitUsage
	}
	cfg, err := integrations.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: secret remove: %v\n", err)
		return exitFailure
	}
	if !cfg.RemoveSecret(rest[0]) {
		_, _ = fmt.Fprintf(stderr, "servitor: secret remove: %q is not declared\n", rest[0])
		return exitFailure
	}
	if err := integrations.Save(cfg, path); err != nil {
		_, _ = fmt.Fprintf(stderr, "servitor: secret remove: %v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "servitor: removed secret %q\n", rest[0])
	return exitOK
}
