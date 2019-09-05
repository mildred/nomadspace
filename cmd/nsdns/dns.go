package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mildred/nomadspace/dns"
)

func stringEnv(name, defVal string) string {
	val, hasVal := os.LookupEnv(name)
	if !hasVal {
		val = defVal
	}
	return val
}

func main() {
	var args nsdns.Args
	flag.StringVar(&args.ConsulServer, "consul-server", "127.0.0.1:8600", "Consul DNS server")
	flag.StringVar(&args.Listen, "listen", stringEnv("NSDNS_LISTEN_ADDR", "127.0.0.1:9653"), "Listen address [NSDNS_LISTEN_ADDR]")
	flag.StringVar(&args.Domain, "domain", stringEnv("NSDNS_DOMAIN", "ns-consul."), "Domain to serve [NSDNS_DOMAIN]")
	flag.StringVar(&args.ConsulDomain, "consul-domain", stringEnv("NSDNS_CONSUL_DOMAIN", "consul."), "Domain to recurse to consul [NSDNS_CONSUL_DOMAIN]")
	flag.Parse()

	if !strings.HasSuffix(args.Domain, ".") {
		args.Domain = args.Domain + "."
	}

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sig
		log.Printf("Received %v, terminating...", s)
		cancel()
		signal.Stop(sig)
	}()

	err := nsdns.Run(ctx, &args)
	if err != nil {
		log.Printf("Error: %v", err)
	}
}

