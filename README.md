NomadSpace
==========

[![Docker Repository on Quay](https://quay.io/repository/mildred/nomadspace/status "Docker Repository on Quay")](https://quay.io/repository/mildred/nomadspace)

NomadSpace is a tool to run multiple Nomad jobs and have them share a common
prefix and settings together. Its history comes from a small shell script I
called nomadns.sh that pre-process hierarchies of Nomad jobs and run them in a
Nomad cluster. See `misc/nomadns.sh`.

Operation mode
--------------

NomadSpace is designed to run as a Nomad job, it takes as parameter a directory
containing a collection of Nomad jobs. At startup it creates a unique and
persistent identifier to prefix all job names with. Then it parses all jobs in
the directory, perfornm a few modification to them, and run them in Nomad.

### Invocation ###

Usual options are:

- `NOMADSPACE_INPUT_DIR` or `--input-dir`: selects the input directory where to
  look for files

- `NOMAD_JOB_NAME` or `--job-name`: the nomad job name nomadspace is running as,
  used to construct a unique nomadspace id. Filled in automatically by Nomad.

DNS options to override jobs:

- `NOMADSPACE_DNS_SERVER` or `--dns-server`: if non empty, override DNS server
  with this value.

- `NOMADSPACE_DNS_SEARCH` or `--dns-search`: if non empty, override DNS search
  with this value. `${NS}` is replaced with the actual namespace string.

    - `NOMADSPACE_DNS_SEARCH_CONSUL=true` or `--dns-search-consul`:
      override DNS search to `service.consul.`.

    - `NOMADSPACE_DNS_SEARCH_NSDNS=true` or `--dns-search-nsdns`:
      override DNS search to `service.${NS}.ns-consul.`. Automatically set if
      `NOMADSPACE_NSDNS` is true.

dnsmasq Options:

- `NOMADSPACE_DNSMASQ` or `--dnsmasq`: Enable dnsmasq server in background.

- `NOMADSPACE_DNSMASQ_LISTEN` or `--dnsmasq-listen`: Change dnsmasq listen
  address and port.


NSDNS Options:

- `NOMADSPACE_NSDNS` or `--nsdns`: Enable nsdns server in background.

- `NSDNS_CONSUL_DOMAIN` or `--nsdns-consul-domain`: to override
  default Consul domain.

- `NSDNS_CONSUL_SERVER` or `--nsdns-consul-server`: to override
  default Consul DNS server address.

- `NSDNS_DOMAIN` or `--nsdns-domain`: to override default nsdns
  domain.

- `NSDNS_LISTEN_ADDR` or `--nsdns-listen`: to override default nsdns
  listening address and port.

Debug options:

- `NOMADSPACE_PRINT_RENDERED` or `--print-rendered`: prints rendered templates.

- `NOMADSPACE_VERBOSE_CONSUL_TEMPLATE` or `--verbose-consul-template`: to
  increase template engine verbosity.


### Job Modifications ###

A unique token is created and added in front of the job name. This token is also
made available as metadata in the job file. It is recommended that Consul keys
that the job uses should use this token as a prefix to avoid name conflicts.


- New values are added to the job:

    - metadata "ns" containing the namespace id (`$NS_ID`)
    - metadata "ns.prefix" containing the namespace prefix (`$NS_ID-`)
    - environment variable `NOMADSPACE_ID` for each task

- Name of some resources are modified:

    - Nomad job name is prefixed by the namespace prefix
    - Consul service names are prefixed by the namespace prefix

- DNS settings are altered if desired:

    - DNS server address is set to `NOMADSPACE_DNS_SERVER`
    - DNS search is set to `NOMADSPACE_DNS_SEARCH` (`${NS}` is replaced by the
      namespace first)

Prior to any of this, the whole job file can be templated using consul-template
using `[[` and `]]` as delimiters. See below for more details on this.


### Nomadspace hierarchies ###

A nomadspace is started by a nomad job running nomadspace. The job name
generally starts by `ns-` but there is no obligation. This job will maintain a
number of sub-jobs, and those sub-jobs can themselves start a new nomadspace.

The nomadspace id added to all job-jobs is generated using the nomadspace
algorithm using the parent job name. Siblings from the parent job can query
Nomad or Consul for child jobs using the templating macro `ns` to find the
correct child nomadspace id.

### Job templating ###

Files can be templated when they end up with `.tmpl`. JSON jobs can be templated
if they end up with `.json.tmpl` and HCL Nomad jobs must end with '.nomad.tmpl'.

Templating is performed with
[consul-template](https://github.com/hashicorp/consul-template#templating-language)
based itself on [Go templates](https://golang.org/pkg/text/template/)
and additional commands are available. This allows nomad job to be updated
automatically on Vault or Consul key change, and react to environment changes.
The templating delimiters are `[[` and `]]`.

The additional environment variables in templating available (through
`env "ENV_NAME"`) are:

- `NS`, `NOMADSPACE_ID`: the NomadSpace ID
- `GEN_DIR`: the template generation dir (so you can import templated files)

The additional commands available are:

#### `ns` ####

Accepts a job name (with its nomadspace prefix) and returns the nomadspace id of
the sub-nomadspace.

Without input, returns the current nomadspace id (taken from the environment)


### Algorithm ###

- Generates a namespace id (called here NS_ID). This namespace id must be unique
  but must also be the same no matter how many times NomadSpace is invoked. it
  uses a hash function generating a 8 character long string DNS compatible fronm
  the NOMAD_JOB_NAME environment variable (the job name for NomadSpace itself)

- Parse all files one by one provided in the input directory, for each:

    - If the file name ends with ".json", parse it as a JSON job
    - If the file name ends with ".nomad", parse it as a Nomad job and convert
      it internally to JSON
    - Perform a few modification to the JSON job (see above)
    - Run the job in Nomad

### Future ideas ###

- Handle `.keys` files and write the specified keys to Consul

nsdns - NomadSpace DNS
======================

Small DNS server that associates:

    *SERVICE.service.DC.dc.NS.ns-consul CNAME *NS-SERVICE.service.DC.consul
    *SERVICE.service.NS.ns-consul       CNAME *NS-SERVICE.service.consul
