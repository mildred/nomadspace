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
	"time"

	"github.com/hashicorp/consul-template/config"
	"github.com/hashicorp/consul-template/manager"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/nomad/api"
	"github.com/mildred/nomadspace/ns"
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
	flag.Parse()

	log.Printf("Starting NomadSpace")

	tmpdir, err := ioutil.TempDir("", "")
	if err != nil {
		return err
	}

	defer os.RemoveAll(tmpdir)

	if inputDir == "" {
		inputDir = "."
	}

	ns := &NomadSpace{
		Id:            ns.Ns(jobName),
		PrintRendered: printRendered,
		RenderedDir:   tmpdir,
		VerboseCT:     verboseCT,
	}

	log.Printf("NomadSpace id:           %v", ns.Id)
	log.Printf("NomadSpace source dir:   %v", inputDir)
	log.Printf("NomadSpace rendered dir: %v", tmpdir)

	ns.nomadClient, err = api.NewClient(api.DefaultConfig())
	if err != nil {
		return err
	}

	return ns.exec(ctx, inputDir)
}

type NomadSpace struct {
	Id            string
	PrintRendered bool
	VerboseCT     bool
	RenderedDir   string

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
			templ, e = ns.readTemplate(fname)
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

	runner.Env["NOMADSPACE_ID"] = ns.Id

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
				if strings.HasSuffix(fname, ".json.tmpl") {
					err = ns.runJSONJob(fname, event.Contents)
				} else {
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

func (ns *NomadSpace) readTemplate(fname string) (*config.TemplateConfig, error) {
	var cfg = config.DefaultTemplateConfig()
	var dst = path.Join(ns.RenderedDir, path.Base(fname))

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
			for _, service := range task.Services {
				service.Name = ns.prefix(service.Name)
			}
		}
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
