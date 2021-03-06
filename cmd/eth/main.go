package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
)

type cmd struct {
	desc  string // command description
	usage string
	do    func(fs *flag.FlagSet) // do it
	fs    flag.FlagSet
}

func fatal(args ...interface{}) {
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(1)
}

func fatalf(f string, args ...interface{}) {
	if len(f) == 0 || f[len(f)-1] != '\n' {
		f += "\n"
	}
	fmt.Fprintf(os.Stderr, f, args...)
	os.Exit(1)
}

var verbose bool

var subcommands = map[string]*cmd{
	"balance": cmdbal,
	"block":   cmdblock,
	"call":    cmdcall,
	"code":    cmdcode,
	"jumptab": cmdjumptab,
	"keygen":  cmdkeygen,
	"keys":    cmdkeylist,
	"post":    cmdpost,
	"read":    cmdread,
	"recover": cmdrecover,
	"sign":    cmdsign,
}

// debugf prints lines prefixed with '+ ' if
// the -v flag is passed as a flag
func debugf(f string, args ...interface{}) {
	if !verbose {
		return
	}
	if len(f) == 0 || f[len(f)-1] != '\n' {
		f += "\n"
	}
	f = "+ " + f
	fmt.Fprintf(os.Stderr, f, args...)
}

func usage() {
	fmt.Println("usage: eth <cmd> <args...>")
	fmt.Println("recognized environment variables:")
	fmt.Println("\tSETH_URL       path to IPC socket or http(s) URL for JSON-RPC server")
	fmt.Println("\tETHER_ADDR     address or name of account (supports a regexp)")
	fmt.Println("\tKEY_PATH       directory in which to search for key files")
	fmt.Println("\t${HSM}_PASS    password to unlock the HSM, e.g. YUBIHSM_PASS=password")
	fmt.Println("\t${HSM}_HINT    HSM-specific hint for probing for the HSM")
	fmt.Println("subcommands:")
	var out [][2]string
	for name, c := range subcommands {
		out = append(out, [2]string{name, c.desc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	for i := range out {
		fmt.Printf("%16s    %s\n", out[i][0], out[i][1])
	}
	os.Exit(1)
}

func main() {
	args := os.Args
	if len(args) == 1 || args[1] == "help" {
		usage()
	}
	cmd, ok := subcommands[args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", args[1])
		usage()
	}

	// every command gets a "-v" flag for debugf output
	cmd.fs.BoolVar(&verbose, "v", false, "verbose")
	cmd.fs.Parse(args[2:])
	cmd.fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage:", cmd.usage)
		cmd.fs.PrintDefaults()
		os.Exit(1)
	}
	cmd.do(&cmd.fs)
}
