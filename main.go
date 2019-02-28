package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strings"
	"time"

	"github.com/hashicorp/consul-template/config"
	"github.com/hashicorp/consul-template/manager"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/nomad/api"
	"github.com/hashicorp/nomad/jobspec"
	"github.com/martinlindhe/base36"
)

const (
	Salt = "PhridcyunDryehorgedraflomcaInGiagyaumOfDyabsyacutNeldUd7"
)

var (
	DefaultLeftDelim  = "[["
	DefaultRightDelim = "]]"
)

func hash(data string) string {
	sum := sha1.Sum([]byte(Salt + data))
	return strings.ToLower(base36.EncodeBytes(sum[:])[0:8])
}

func main() {
	var err error
	var inputDir string
	var jobName string

	flag.StringVar(&inputDir,
		"input-dir", os.Getenv("NOMADSPACE_INPUT_DIR"),
		"Input directory where to find Nomad jobs [NOMADSPACE_INPUT_DIR]")
	flag.StringVar(&jobName,
		"job-name", os.Getenv("NOMAD_JOB_NAME"),
		"Job name to infer NomadSpace ID [NOMAD_JOB_NAME]")
	flag.Parse()

	ns := &NomadSpace{
		Id: hash(jobName),
	}

	if inputDir == "" {
		inputDir = "."
	}

	log.Printf("Starting NomadSpace %v", ns.Id)

	ns.nomadClient, err = api.NewClient(api.DefaultConfig())
	if err != nil {
		log.Fatal(err)
	}

	err = ns.exec(inputDir)
	if err != nil {
		log.Fatal(err)
	}
}

type NomadSpace struct {
	Id string

	nomadClient *api.Client
}

func (ns *NomadSpace) exec(inputDir string) error {
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
			templ, e = readTemplate(fname)
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

	runner, err := manager.NewRunner(cfg, false, false)
	if err != nil {
		return err
	}

	runner.Env = map[string]string{
		"NOMADSPACE_ID": ns.Id,
	}

	now := time.Now()
	go runner.Start()

	for {
		var next = now
		<-runner.RenderEventCh()
		for _, event := range runner.RenderEvents() {
			if now.After(event.UpdatedAt) {
				continue
			} else if next.Before(event.UpdatedAt) {
				next = event.UpdatedAt
			}

			fname := path.Base(*event.TemplateConfigs[0].Source)
			if event.MissingDeps != nil {
				for _, dep := range event.MissingDeps.List() {
					log.Printf("Missing dep for %v: %v", fname, dep)
				}
			}

			if len(event.Contents) > 0 {
				log.Printf("Rendered %v", fname)
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

func readNomad(fname string) (*api.Job, error) {
	f, err := os.Open(fname)
	if err != nil {
		return nil, err
	}

	defer f.Close()

	res, err := jobspec.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse %v, %v", fname, err)
	}

	return res, nil
}

func readTemplate(fname string) (*config.TemplateConfig, error) {
	var cfg = config.DefaultTemplateConfig()

	cfg.Source = &fname
	cfg.LeftDelim = &DefaultLeftDelim
	cfg.RightDelim = &DefaultRightDelim
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
	name := ns.prefix(*job.Name)
	job.Name = &name
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
	log.Printf("Submit %v", fname)
	res, _, err := ns.nomadClient.Jobs().Register(job, nil)
	log.Printf("Submitted %v: eval %v", fname, res.EvalID)
	if len(res.Warnings) > 0 {
		log.Printf("Submitted %v: WARNING %v", fname, res.Warnings)
	}
	return err
}
