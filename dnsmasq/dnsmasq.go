package dnsmasq

import (
	"context"
	"fmt"
	"strings"
	"os"
	"os/exec"
	"log"
)

type Args struct {
	ConsulEnable bool
	NsdnsEnable  bool
	ConsulAddr   string
	NsdnsAddr    string
	NsdnsDomain  string
	ConsulDomain string
	Listen       string
	ExtraArgs    []string
}

func dnsmasqDomain(d string) string {
	if len(d) > 0 && d[len(d)-1] == '.' {
		return d[:len(d)-1]
	} else {
		return d
	}
}

func dnsmasqHostPort(h string) string {
	return strings.ReplaceAll(h, ":", "#")
}

func Run(ctx context.Context, l *log.Logger, args *Args) error {
	path, err := exec.LookPath("dnsmasq")
	if err != nil {
		return fmt.Errorf("Cannot find dnsmasq in PATH, %v", err)
	}

	var cmdArgs []string = []string{
		"dnsmasq",
		"--keep-in-foreground",
		"--log-facility=-",
		"--cache-size=0",
		"--no-negcache",
		"--dns-forward-max=500",
	}

	if args.ConsulEnable {
		cmdArgs = append(cmdArgs, fmt.Sprintf("--server=/%s/%s",
			dnsmasqDomain(args.ConsulDomain),
			dnsmasqHostPort(args.ConsulAddr)))
	}

	if args.NsdnsEnable {
		cmdArgs = append(cmdArgs, fmt.Sprintf("--server=/%s/%s",
			dnsmasqDomain(args.NsdnsDomain),
			dnsmasqHostPort(args.NsdnsAddr)))
	}

	cmdArgs = append(cmdArgs, args.ExtraArgs...)

	l.Print("Starting dnsmasq")
	for _, arg := range cmdArgs {
		l.Printf("\t%s", arg)
	}

	cmd := exec.Cmd{
		Path: path,
		Args: cmdArgs,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	return cmd.Run()
}
