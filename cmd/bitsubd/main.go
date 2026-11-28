// Command bitsubd is the always-on bitsub service: the subscription store,
// reminders, calendar and mail through gog, FX rates, MCP tools and the
// plugin JSON API.
//
//	bitsubd [-config path]                 serve (default)
//	bitsubd [-config path] token add NAME  print a new token once, store its hash
//	bitsubd [-config path] token list
//	bitsubd [-config path] token revoke NAME
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"syscall"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/httpapi"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bitsubd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", DefaultConfigPath(), "config file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var err error
	switch rest := fs.Args(); {
	case len(rest) == 0 || (len(rest) == 1 && rest[0] == "serve"):
		err = serveMain(*configPath)
	case rest[0] == "token":
		err = tokenCmd(*configPath, rest[1:], stdout)
	default:
		err = fmt.Errorf("unknown command %q", rest[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, "bitsubd:", err)
		return 1
	}
	return 0
}

func serveMain(configPath string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	d, err := newDaemon(cfg, deps{})
	if err != nil {
		return err
	}
	lns, err := listen(cfg.Listen)
	if err != nil {
		d.st.Close()
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return d.serve(ctx, lns)
}

func tokenCmd(configPath string, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: token add|list|revoke [NAME]")
	}
	cfg, err := LoadConfig(configPath)
	if errors.Is(err, os.ErrNotExist) && args[0] == "add" {
		cfg, err = Defaults(), nil
	}
	if err != nil {
		return err
	}
	switch {
	case args[0] == "list" && len(args) == 1:
		for _, t := range cfg.Tokens {
			fmt.Fprintln(stdout, t.Name)
		}
		return nil
	case args[0] == "add" && len(args) == 2:
		name := args[1]
		if !tokenName.MatchString(name) {
			return fmt.Errorf("token name %q: use 1–40 of a-z, 0-9 and -", name)
		}
		if slices.ContainsFunc(cfg.Tokens, func(t httpapi.Token) bool { return t.Name == name }) {
			return fmt.Errorf("token %q exists; revoke it first", name)
		}
		plain, sha, err := httpapi.GenerateToken()
		if err != nil {
			return err
		}
		cfg.Tokens = append(cfg.Tokens, httpapi.Token{Name: name, SHA256: sha})
		if err := SaveConfig(configPath, cfg); err != nil {
			return err
		}
		fmt.Fprintln(stdout, plain)
		return nil
	case args[0] == "revoke" && len(args) == 2:
		n := len(cfg.Tokens)
		cfg.Tokens = slices.DeleteFunc(cfg.Tokens, func(t httpapi.Token) bool { return t.Name == args[1] })
		if len(cfg.Tokens) == n {
			return fmt.Errorf("no token named %q", args[1])
		}
		return SaveConfig(configPath, cfg)
	}
	return errors.New("usage: token add|list|revoke [NAME]")
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return info.Main.Version
}
