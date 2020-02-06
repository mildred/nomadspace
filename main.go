package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
	"sort"
	"time"

	"github.com/hashicorp/consul-template/config"
	"github.com/hashicorp/consul-template/manager"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/nomad/api"
	"github.com/mildred/nomadspace/dns"
	"github.com/mildred/nomadspace/ns"
	"github.com/mildred/nomadspace/waitgroup"
)

var (
	DefaultLeftDelim  = "[["
	DefaultRightDelim = "]]"
)

func boolEnv(name string, defVal bool) bool {
	val := os.Getenv(name)
	res, err := strconv.ParseBool(val)
	if err != nil || val == "" {
		res = defVal
	}
	return res
}

func stringEnv(name, defVal string) string {
	val, hasVal := os.LookupEnv(name)
	if !hasVal {
		val = defVal
	}
	return val
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sig
		log.Printf("Received %v, terminating...", s)
		cancel()
		signal.Stop(sig)
	}()

	err := run(ctx)
	if err != nil {
		log.Fatalf("ERROR: %v", err)
	}
}

func run(ctx context.Context) error {
	var err error
	var inputDir string
	var jobName string
	var printRendered bool
	var verboseCT bool
	var dnsServer string
	var dnsSearch string
	var dnsSearchNsDNS bool
	var dnsSearchConsul bool
	var nsdnsEnable bool
	var nsdnsArgs nsdns.Args

	flag.StringVar(&inputDir,
		"input-dir", os.Getenv("NOMADSPACE_INPUT_DIR"),
		"Input directory where to find Nomad jobs [NOMADSPACE_INPUT_DIR]")
	flag.StringVar(&jobName,
		"job-name", os.Getenv("NOMAD_JOB_NAME"),
		"Job name to infer NomadSpace ID [NOMAD_JOB_NAME]")
	flag.BoolVar(&printRendered,
		"print-rendered", boolEnv("NOMADSPACE_PRINT_RENDERED", false),
		"Print rendered templates [NOMADSPACE_PRINT_RENDERED]")
	flag.BoolVar(&verboseCT,
		"verbose-consul-template", boolEnv("NOMADSPACE_VERBOSE_CONSUL_TEMPLATE", false),
		"Print consul-template logs [NOMADSPACE_VERBOSE_CONSUL_TEMPLATE]")
	flag.StringVar(&dnsServer,
		"dns-server", stringEnv("NOMADSPACE_DNS_SERVER", ""),
		"DNS server to override in jobs [NOMADSPACE_DNS_SERVER]")
	flag.StringVar(&dnsSearch,
		"dns-search", stringEnv("NOMADSPACE_DNS_SEARCH", ""),
		"DNS search, ${NS} replaced with namespace [NOMADSPACE_DNS_SEARCH]")
	flag.BoolVar(&dnsSearchNsDNS,
		"dns-search-nsdns", boolEnv("NOMADSPACE_DNS_SEARCH_NSDNS", boolEnv("NOMADSPACE_NSDNS", false)),
		"Alias for --dns-search=service.${NS}.ns-consul. [NOMADSPACE_DNS_SEARCH_NSDNS, NOMADSPACE_NSDNS]")
	flag.BoolVar(&dnsSearchConsul,
		"dns-search-consul", boolEnv("NOMADSPACE_DNS_SEARCH_CONSUL", false),
		"Alias for --dns-search=service.consul. [NOMADSPACE_DNS_SEARCH_CONSUL]")
	flag.BoolVar(&nsdnsEnable,
		"nsdns", boolEnv("NOMADSPACE_NSDNS", false),
		"Start DNS server and set --dns-search (--dns-server should be set to reachable IP address) [NOMADSPACE_NSDNS]")
	flag.StringVar(&nsdnsArgs.ConsulServer,
		"nsdns-consul-server", "127.0.0.1:8600",
		"Consul DNS server")
	flag.StringVar(&nsdnsArgs.Listen,
		"nsdns-listen", stringEnv("NSDNS_LISTEN_ADDR", "127.0.0.1:9653"),
		"Listen address [NSDNS_LISTEN_ADDR]")
	flag.StringVar(&nsdnsArgs.Domain,
		"nsdns-domain", stringEnv("NSDNS_DOMAIN", "ns-consul."),
		"Domain to serve [NSDNS_DOMAIN]")
	flag.StringVar(&nsdnsArgs.ConsulDomain,
		"nsdns-consul-domain", stringEnv("NSDNS_CONSUL_DOMAIN", "consul."),
		"Domain to recurse to consul [NSDNS_CONSUL_DOMAIN]")
	flag.Parse()

	if dnsSearchConsul {
		if dnsSearchNsDNS || dnsSearch != "" {
			return fmt.Errorf("Cannot set both --dns-search-consul with other --dns-search options")
		}
		dnsSearch = "service.consul."
	} else if dnsSearchNsDNS {
		if dnsSearchConsul || dnsSearch != "" {
			return fmt.Errorf("Cannot set both --dns-search-nsdns with other --dns-search options")
		}
		dnsSearch = "service.${NS}.ns-consul."
	}

	if nsdnsEnable && dnsSearch == "" {
		dnsSearch = "service.${NS}.ns-consul."
	}

	log.Printf("Starting NomadSpace")

	tmpdir, err := ioutil.TempDir("", "")
	if err != nil {
		return err
	}

	defer os.RemoveAll(tmpdir)

	if inputDir == "" {
		inputDir = "."
	}

	nsId := ns.Ns(jobName)
	ns := &NomadSpace{
		Id:            nsId,
		PrintRendered: printRendered,
		RenderedDir:   tmpdir,
		VerboseCT:     verboseCT,
		DNSSearch:     strings.Replace(dnsSearch, "${NS}", nsId, -1),
		DNSServer:     dnsServer,
	}

	log.Printf("NomadSpace id:           %v", ns.Id)
	log.Printf("NomadSpace source dir:   %v", inputDir)
	log.Printf("NomadSpace rendered dir: %v", tmpdir)

	ns.nomadClient, err = api.NewClient(api.DefaultConfig())
	if err != nil {
		return err
	}

	wg := waitgroup.New()

	if nsdnsEnable {
		wg.Start(func() error {
			return nsdns.Run(ctx, &nsdnsArgs)
		})
	}

	wg.Start(func() error {
		return ns.exec(ctx, inputDir)
	})

	return wg.Wait()
}

type NomadSpace struct {
	Id            string
	PrintRendered bool
	VerboseCT     bool
	RenderedDir   string
	DNSSearch     string
	DNSServer     string

	nomadClient *api.Client
}

func (ns *NomadSpace) exec(ctx context.Context, inputDir string) error {
	f, err := os.Open(inputDir)
	if err != nil {
		return err
	}

	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}

	sort.Strings(names)

	log.Printf("Found %d files in input dir %s", len(names), inputDir)

	var jobs = map[string]*api.Job{}
	var cfg *config.Config = config.DefaultConfig()

	for _, name := range names {
		var job *api.Job
		var e error
		var fname = path.Join(inputDir, name)
		if strings.HasSuffix(name, ".json") {
			log.Printf("Read JSON %v", fname)
			job, e = readJSON(fname)
		} else if strings.HasSuffix(name, ".nomad") {
			log.Printf("Read Nomad %v", fname)
			job, e = readNomadAPI(ns.nomadClient, fname)
		} else if strings.HasSuffix(name, ".tmpl") {
			log.Printf("Read Template %v", fname)
			var templ *config.TemplateConfig
			templ, e = ns.readTemplate(fname, path.Base(fname[:len(fname)-5]))
			if e == nil {
				*cfg.Templates = append(*cfg.Templates, templ)
			}
		} else {
			log.Printf("Ignore %v", fname)
		}
		if e != nil {
			err = multierror.Append(err, e).ErrorOrNil()
		} else if job != nil {
			jobs[name] = job
		}
	}
	if err != nil {
		return err
	}

	for fname, job := range jobs {
		e := ns.runJob(fname, job)
		if e != nil {
			err = multierror.Append(err, e).ErrorOrNil()
		}
	}
	if err != nil {
		return err
	}

	if len(*cfg.Templates) == 0 {
		log.Printf("Jobs are submitted, waiting forever...")
		<-ctx.Done()
		return ctx.Err()
	}

	runner, err := manager.NewRunner(cfg, false)
	if err != nil {
		return err
	}

	runner.Env = map[string]string{}

	if !ns.VerboseCT {
		runner.SetOutStream(ioutil.Discard)
		runner.SetErrStream(ioutil.Discard)
	}

	for _, env := range os.Environ() {
		vals := strings.SplitN(env, "=", 2)
		runner.Env[vals[0]] = vals[1]
	}

	runner.Env["GEN_DIR"] = ns.RenderedDir
	runner.Env["NOMADSPACE_ID"] = ns.Id
	runner.Env["NS"] = ns.Id

	now := time.Now()
	go runner.Start()

	for {
		var next = now
		select {
		case <-runner.DoneCh:
			log.Printf("Template done.")
			break
		case err := <-runner.ErrCh:
			log.Printf("Template error: %v", err)
			return err
		case <-runner.TemplateRenderedCh():
			log.Printf("Template rendered.")
		case <-runner.RenderEventCh():
			log.Printf("Template event.")
		}
		for i, event := range runner.RenderEvents() {
			if now.After(event.UpdatedAt) {
				log.Printf("Received event %v: updated before last check (%v < %v)", i, event.UpdatedAt, now)
				continue
			} else if next.Before(event.UpdatedAt) {
				log.Printf("Received event %v: updated at %v", i, event.UpdatedAt)
				next = event.UpdatedAt
			}

			fname := path.Base(*event.TemplateConfigs[0].Source)
			if event.MissingDeps != nil {
				for _, dep := range event.MissingDeps.List() {
					log.Printf("Missing dep for %v: %v", fname, dep)
				}
			}

			if len(event.Contents) > 0 {
				if ns.PrintRendered {
					log.Printf("Rendered %v:\n%s", fname, string(event.Contents))
				} else {
					log.Printf("Rendered %v", fname)
				}
				err = nil
				if strings.HasSuffix(fname, ".json.tmpl") {
					err = ns.runJSONJob(fname, event.Contents)
				} else if strings.HasSuffix(fname, ".nomad.tmpl") {
					err = ns.runNomadJob(fname, event.Contents)
				}
				if err != nil {
					log.Printf("ERROR rendering %v: %v", fname, err)
				}
			}
		}
		log.Printf("Handled events updated last at %v", next)
		now = next
	}

	return nil
}

func readJSON(fname string) (*api.Job, error) {
	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}

	defer f.Close()

	var res api.Job
	err = json.NewDecoder(f).Decode(&res)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse %v, %v", fname, err)
	}

	return &res, nil
}

func readNomadAPI(nc *api.Client, fname string) (*api.Job, error) {
	data, err := ioutil.ReadFile(fname)
	if err != nil {
		return nil, err
	}

	job, err := nc.Jobs().ParseHCL(string(data), true)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse %v, %v", fname, err)
	}

	return job, nil
}

func (ns *NomadSpace) readTemplate(fname, dstname string) (*config.TemplateConfig, error) {
	var cfg = config.DefaultTemplateConfig()
	var dst = path.Join(ns.RenderedDir, dstname)

	cfg.Source = &fname
	cfg.LeftDelim = &DefaultLeftDelim
	cfg.RightDelim = &DefaultRightDelim
	cfg.Destination = &dst
	cfg.Finalize()

	return cfg, nil
}

func (ns *NomadSpace) runJSONJob(fname string, content []byte) error {
	var job api.Job

	r := bytes.NewReader(content)
	err := json.NewDecoder(r).Decode(&job)
	if err != nil {
		return fmt.Errorf("Failed to parse rendered %v, %v", fname, err)
	}

	return ns.runJob(fname, &job)
}

func (ns *NomadSpace) runNomadJob(fname string, content []byte) error {
	job, err := ns.nomadClient.Jobs().ParseHCL(string(content), true)
	if err != nil {
		return fmt.Errorf("Failed to parse rendered %v, %v", fname, err)
	}

	return ns.runJob(fname, job)
}

func (ns *NomadSpace) prefix(name string) string {
	if !strings.HasPrefix(name, ns.Id+"-") {
		name = ns.Id + "-" + name
	}
	return name
}

func (ns *NomadSpace) namespaceJob(job *api.Job) {
	name := ns.prefix(*job.ID)
	job.ID = &name
	if job.Meta == nil {
		job.Meta = map[string]string{}
	}
	job.Meta["ns"] = ns.Id
	job.Meta["ns.prefix"] = ns.Id + "-"
	for _, group := range job.TaskGroups {
		for _, task := range group.Tasks {
			if task.Env == nil {
				task.Env = map[string]string{}
			}
			task.Env["NOMADSPACE_ID"] = ns.Id
			switch task.Driver {
			case "docker", "rkt":
				if ns.DNSSearch != "" {
					searchDomains := toStringList(task.Config["dns_search_domains"])
					searchDomains = append(searchDomains, ns.DNSSearch)
					task.Config["dns_search_domains"] = searchDomains
				}
				if ns.DNSServer != "" {
					servers := toStringList(task.Config["dns_servers"])
					servers = append(servers, ns.DNSServer)
					task.Config["dns_servers"] = servers
				}
			}
			for _, service := range task.Services {
				service.Name = ns.prefix(service.Name)
			}
		}
	}
}

func toStringList(val interface{}) []string {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	default:
		return nil
	}
}

func (ns *NomadSpace) runJob(fname string, job *api.Job) error {
	ns.namespaceJob(job)
	log.Printf("Submit %v: %v", fname, *job.ID)
	res, _, err := ns.nomadClient.Jobs().Register(job, nil)
	if err != nil {
		return fmt.Errorf("Failed to submit %v, %v", fname, err)
	}
	log.Printf("Submitted %v: eval %v", fname, res.EvalID)
	if len(res.Warnings) > 0 {
		log.Printf("Submitted %v: WARNING %v", fname, res.Warnings)
	}
	return nil
}
