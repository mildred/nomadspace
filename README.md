NomadSpace
==========

NomadSpace is a tool to run multiple Nomad jobs and have them share a common
prefix and settings together. Its history comes from a small shell script I
called nomadns.sh that pre-process hierarchies of Nomad jobs and run them in a
Nomad cluster. See `misc/nomadns.sh`.

Operation mode
--------------

NomadSpace is designed to run as a Nomad job, it takes as parameter a .zip file
containing a collection of Nomad jobs. At startup it creates a unique and
persistent identifier to prefix all job names with. Then it parses all jobs in
the .zip file, perfornm a few modification to them, and run them in Nomad.

### Job Modifications ###

A unique token is created and added in front of the job name. This token is also
made available as metadata in the job file. It is recommended that Consul keys
that the job uses should use this token as a prefix to avoid name conflicts.

### Algorithm ###

- Generates a namespace id (called here NS_ID). This namespace id must be unique
  but must also be the same no matter how many times NomadSpace is invoked. it
  uses a hash function generating a 8 character long string DNS compatible fronm
  the NOMAD_JOB_NAME environment variable (the job name for NomadSpace itself)

- Parse all files one by one provided in the input directory, for each:

    - If the file name ends with ".json", parse it as a JSON job
    - If the file name ends with ".nomad", parse it as a Nomad job and convert
      it internally to JSON
    - Perform a few modification to the JSON job:
        - prefix the job name by "$NS_ID-"
        - add a metadata "ns" with "$NS_ID"
        - add a metadata "ns.prefix" with "$NS_ID-"
        - add environment variables for each task `NOMADSPACE_ID`
        - prefix each service stanza by "$NS_ID-"
    - Run the job in Nomad
