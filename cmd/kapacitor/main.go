package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/influxdb/kapacitor"
)

// These variables are populated via the Go linker.
var (
	version string = "v0.1"
	commit  string
	branch  string
)

var l = log.New(os.Stderr, "[run] ", log.LstdFlags)

var usageStr = `
Usage: kapacitor [command] [args]

Commands:

	record   record the result of a query or a snapshot of the current stream data.
	define   create/update a task.
	replay   replay a recording to a task.
	enable   enable and start running a task with live data.
	disable  stop running a task.
	push     publish a task definition to another Kapacitor instance.
	delete   delete a task or a recording.
	list     list information about tasks or recordings.
	help     get help for a command.
	level    sets the logging level on the kapacitord server.
	version  displays the Kapacitor version info.
`

func usage() {
	fmt.Fprintln(os.Stderr, usageStr)
	os.Exit(1)
}

func main() {

	if len(os.Args) == 1 {
		fmt.Fprintln(os.Stderr, "Error: Must pass a command.")
		usage()
	}

	command := os.Args[1]
	args := os.Args[2:]
	var commandF func(args []string) error
	var commandArgs []string
	switch command {
	case "help":
		commandArgs = args
		commandF = doHelp
	case "record":
		if len(args) == 0 {
			recordFlags.Usage()
			os.Exit(2)
		}
		recordFlags.Parse(args[1:])
		commandArgs = args[0:1]
		commandF = doRecord
	case "define":
		defineFlags.Parse(args)
		commandArgs = defineFlags.Args()
		commandF = doDefine
	case "replay":
		replayFlags.Parse(args)
		commandArgs = replayFlags.Args()
		commandF = doReplay
	case "enable":
		commandArgs = args
		commandF = doEnable
	case "disable":
		commandArgs = args
		commandF = doDisable
	case "delete":
		commandArgs = args
		commandF = doDelete
	case "list":
		commandArgs = args
		commandF = doList
	case "level":
		commandArgs = args
		commandF = doLevel
	case "version":
		commandArgs = args
		commandF = doVersion
	default:
		fmt.Fprintln(os.Stderr, "Unknown command", command)
		usage()
	}

	err := commandF(commandArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
}

// Init flag sets
func init() {
	replayFlags.Usage = replayUsage
	defineFlags.Usage = defineUsage
	recordFlags.Usage = recordUsage
}

// helper methods

type responseError struct {
	Err string `json:"Error"`
}

func (e responseError) Error() string {
	return e.Err
}

// Help

func helpUsage() {
	var u = "Usage: kapacitor help [command]\n"
	fmt.Fprintln(os.Stderr, u)
}

func doHelp(args []string) error {
	if len(args) == 1 {
		command := args[0]
		switch command {
		case "record":
			recordFlags.Usage()
		case "define":
			defineFlags.Usage()
		case "replay":
			replayFlags.Usage()
		case "enable":
			enableUsage()
		case "disable":
			disableUsage()
		case "delete":
			deleteUsage()
		case "list":
			listUsage()
		case "level":
			levelUsage()
		case "help":
			helpUsage()
		case "version":
			versionUsage()
		default:
			fmt.Fprintln(os.Stderr, "Unknown command", command)
			usage()
		}
	} else {
		helpUsage()
	}
	return nil
}

// Record
var (
	recordFlags = flag.NewFlagSet("record", flag.ExitOnError)
	raddr       = recordFlags.String("addr", "", "the URL address of the InfluxDB server. If recording a batch or query.")
	rname       = recordFlags.String("name", "", "the name of a task. If recording a batch")
	rstart      = recordFlags.String("start", "", "the start time of a task query.")
	rnum        = recordFlags.Int("num", 1, "the number of periods to query. If recording a batch")

	rquery = recordFlags.String("query", "", "the query to record. If recording a query.")
	rtype  = recordFlags.String("type", "", "the type of the recording to save (streamer|batcher). If recording a query.")

	rdur = recordFlags.Duration("duration", time.Minute*5, "how long to record the data stream. If recording a stream.")
)

func recordUsage() {
	var u = `Usage: kapacitor record [batch|stream|query] [options]

	Record the result of a InfluxDB query or a snapshot of the live data stream.

	Prints the recording ID on exit.

	Recordings have types like tasks. If recording a raw query you must specify the desired type.

	See 'kapacitor help replay' for how to replay a recording.

Examples:

	$ kapacitor record stream -duration 1m

		This records the live data stream for 1 minute.
	
	$ kapacitor record batch -addr 'http://localhost:8086' -name cpu_idle -start 2015-09-01T00:00:00Z -num 10
		
		This records the result of the query defined in task 'cpu_idle' and runs the query 10 times
		starting at time 'start' and incrementing by the period defined in the task.

	$ kapacitor record query -addr 'http://localhost:8086' -query "select value from cpu_idle where time > now() - 1h and time < now()" -type streamer

		This records the result of the query and stores it as a stream recording. Use -type batcher to store as batch recording.

Options:
`
	fmt.Fprintln(os.Stderr, u)
	recordFlags.PrintDefaults()
}

func doRecord(args []string) error {

	v := url.Values{}
	v.Add("type", args[0])
	switch args[0] {
	case "stream":
		v.Add("duration", rdur.String())
	case "batch":
		v.Add("name", *rname)
		v.Add("start", *rstart)
		v.Add("num", strconv.FormatInt(int64(*rnum), 10))
		v.Add("addr", *raddr)
	case "query":
		v.Add("qtype", *rtype)
		v.Add("addr", *raddr)
	default:
		return fmt.Errorf("Unknown record type %q, expected 'stream' or 'query'", args[0])
	}
	r, err := http.Post("http://localhost:9092/record?"+v.Encode(), "application/octetstream", nil)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	// Decode valid response
	type resp struct {
		RecordingID string `json:"RecordingID"`
		Error       string `json:"Error"`
	}
	d := json.NewDecoder(r.Body)
	rp := resp{}
	d.Decode(&rp)
	if rp.Error != "" {
		return errors.New(rp.Error)
	}
	fmt.Println(rp.RecordingID)
	return nil
}

// Define
var (
	defineFlags = flag.NewFlagSet("define", flag.ExitOnError)
	dname       = defineFlags.String("name", "", "the task name")
	dtick       = defineFlags.String("tick", "", "path to the TICK script")
	dtype       = defineFlags.String("type", "", "the task type (streamer|batcher)")
)

func defineUsage() {
	var u = `Usage: kapacitor define [options]

Create or update a task.

A task is defined via a TICK script that defines the data processing pipeline of the task.

Options:
`
	fmt.Fprintln(os.Stderr, u)
	defineFlags.PrintDefaults()
}

func doDefine(args []string) error {

	if *dtick == "" || *dname == "" || *dtick == "" {
		fmt.Fprintln(os.Stderr, "Must pass name,tick and type options.")
		defineFlags.Usage()
		os.Exit(2)
	}

	f, err := os.Open(*dtick)
	if err != nil {
		return err
	}
	v := url.Values{}
	v.Add("name", *dname)
	v.Add("type", *dtype)
	r, err := http.Post("http://localhost:9092/task?"+v.Encode(), "application/octetstream", f)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	// Decode valid response
	type resp struct {
		Error string `json:"Error"`
	}
	d := json.NewDecoder(r.Body)
	rp := resp{}
	d.Decode(&rp)
	if rp.Error != "" {
		return errors.New(rp.Error)
	}
	return nil
}

// Replay
var (
	replayFlags = flag.NewFlagSet("replay", flag.ExitOnError)
	rtname      = replayFlags.String("name", "", "the task name")
	rid         = replayFlags.String("id", "", "the recording ID")
	rfast       = replayFlags.Bool("fast", false, "whether to replay the data as fast as possible. If false, replay the data in real time")
)

func replayUsage() {
	var u = `Usage: kapacitor replay [options]

Replay a recording to a task. Waits until the task finishes.

See 'kapacitor help record' for how to create a replay.
See 'kapacitor help define' for how to create a task.

Options:
`
	fmt.Fprintln(os.Stderr, u)
	replayFlags.PrintDefaults()
}

func doReplay(args []string) error {

	v := url.Values{}
	v.Add("name", *rtname)
	v.Add("id", *rid)
	if *rfast {
		v.Add("clock", "fast")
	}
	r, err := http.Post("http://localhost:9092/replay?"+v.Encode(), "application/octetstream", nil)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	// Decode valid response
	type resp struct {
		Error string `json:"Error"`
	}
	d := json.NewDecoder(r.Body)
	rp := resp{}
	d.Decode(&rp)
	if rp.Error != "" {
		return errors.New(rp.Error)
	}
	return nil
}

// Enable
func enableUsage() {
	var u = `Usage: kapacitor enable [task name...]

	Enable and start a task running from the live data.
`
	fmt.Fprintln(os.Stderr, u)
}

func doEnable(args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Must pass at least one task name")
		enableUsage()
		os.Exit(2)
	}

	for _, name := range args {
		v := url.Values{}
		v.Add("name", name)
		r, err := http.Post("http://localhost:9092/enable?"+v.Encode(), "application/octetstream", nil)
		if err != nil {
			return err
		}
		defer r.Body.Close()
		// Decode valid response
		type resp struct {
			Error string `json:"Error"`
		}
		d := json.NewDecoder(r.Body)
		rp := resp{}
		d.Decode(&rp)
		if rp.Error != "" {
			return errors.New(rp.Error)
		}
	}
	return nil
}

// Disable

func disableUsage() {
	var u = `Usage: kapacitor disable [task name...]

	Disable and stop a task running.
`
	fmt.Fprintln(os.Stderr, u)
}

func doDisable(args []string) error {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Must pass at least one task name")
		disableUsage()
		os.Exit(2)
	}

	for _, name := range args {
		v := url.Values{}
		v.Add("name", name)
		r, err := http.Post("http://localhost:9092/disable?"+v.Encode(), "application/octetstream", nil)
		if err != nil {
			return err
		}
		defer r.Body.Close()
		// Decode valid response
		type resp struct {
			Error string `json:"Error"`
		}
		d := json.NewDecoder(r.Body)
		rp := resp{}
		d.Decode(&rp)
		if rp.Error != "" {
			return errors.New(rp.Error)
		}
		return nil
	}
	return nil
}

// List

func listUsage() {
	var u = `Usage: kapacitor list (tasks|recordings) [task name|recording ID]...

List tasks or recordings and their current state.

If no tasks are given then all tasks are listed. Same for recordings.
If a set of task names or recordings IDs is provided only those entries will be listed.
`
	fmt.Fprintln(os.Stderr, u)
}

func doList(args []string) error {

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Must specify 'tasks' or 'recordings'")
		listUsage()
		os.Exit(2)
	}

	switch args[0] {
	case "tasks":
		tasks := strings.Join(args[1:], ",")
		v := url.Values{}
		v.Add("tasks", tasks)
		r, err := http.Get("http://localhost:9092/tasks?" + v.Encode())
		if err != nil {
			return err
		}
		defer r.Body.Close()
		// Decode valid response
		type resp struct {
			Error string `json:"Error"`
			Tasks []struct {
				Name    string
				Type    kapacitor.TaskType
				Enabled bool
			} `json:"Tasks"`
		}
		d := json.NewDecoder(r.Body)
		rp := resp{}
		d.Decode(&rp)
		if rp.Error != "" {
			return errors.New(rp.Error)
		}

		outFmt := "%-30s%-10v%-10v\n"
		fmt.Fprintf(os.Stdout, outFmt, "Name", "Type", "Enabled")
		for _, t := range rp.Tasks {
			fmt.Fprintf(os.Stdout, outFmt, t.Name, t.Type, t.Enabled)
		}
	case "recordings":

		rids := strings.Join(args[1:], ",")
		v := url.Values{}
		v.Add("rids", rids)
		r, err := http.Get("http://localhost:9092/recordings?" + v.Encode())
		if err != nil {
			return err
		}
		defer r.Body.Close()
		// Decode valid response
		type resp struct {
			Error      string `json:"Error"`
			Recordings []struct {
				ID   string
				Type kapacitor.TaskType
				Size int64
			} `json:"Recordings"`
		}
		d := json.NewDecoder(r.Body)
		rp := resp{}
		d.Decode(&rp)
		if rp.Error != "" {
			return errors.New(rp.Error)
		}

		outFmt := "%-40s%-10v%15.2f\n"
		fmt.Fprintf(os.Stdout, "%-40s%-10s%15s\n", "ID", "Type", "Size (MB)")
		for _, r := range rp.Recordings {
			fmt.Fprintf(os.Stdout, outFmt, r.ID, r.Type, float64(r.Size)/1024.0/1024.0)
		}
	}
	return nil

}

// Delete
func deleteUsage() {
	var u = `Usage: kapacitor delete (task|recording) [task name|recording ID]...

	Delete a task or recording.
	
	If a task is enabled it will be disabled and then deleted.
`
	fmt.Fprintln(os.Stderr, u)
}

func doDelete(args []string) error {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Must pass at least one task name or recording ID")
		enableUsage()
		os.Exit(2)
	}

	var baseURL string
	var paramName string
	switch args[0] {
	case "task":
		baseURL = "http://localhost:9092/task"
		paramName = "name"
	case "recording":
		baseURL = "http://localhost:9092/recording"
		paramName = "rid"
	}

	l.Println(args)

	for _, arg := range args[1:] {
		v := url.Values{}
		v.Add(paramName, arg)
		req, err := http.NewRequest("DELETE", baseURL+v.Encode(), nil)
		if err != nil {
			return err
		}
		client := &http.Client{}
		r, err := client.Do(req)
		if err != nil {
			return err
		}
		defer r.Body.Close()
		// Decode valid response
		type resp struct {
			Error string `json:"Error"`
		}
		d := json.NewDecoder(r.Body)
		rp := resp{}
		d.Decode(&rp)
		if rp.Error != "" {
			return errors.New(rp.Error)
		}
	}

	return nil
}

// Level
func levelUsage() {
	var u = `Usage: kapacitor level (debug|info|warn|error)

	Sets the logging level on the kapacitord server.
`
	fmt.Fprintln(os.Stderr, u)
}

func doLevel(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Must pass a log level")
		levelUsage()
		os.Exit(2)
	}
	v := url.Values{}
	v.Add("level", args[0])
	r, err := http.Post("http://localhost:9092/loglevel?"+v.Encode(), "text/plain", nil)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	// Decode valid response
	type resp struct {
		Error string `json:"Error"`
	}
	d := json.NewDecoder(r.Body)
	rp := resp{}
	d.Decode(&rp)
	if rp.Error != "" {
		return errors.New(rp.Error)
	}
	return nil
}

// Version
func versionUsage() {
	var u = `Usage: kapacitor version

	Print version info.
`
	fmt.Fprintln(os.Stderr, u)
}

func doVersion(args []string) error {
	fmt.Fprintf(os.Stdout, "Kapacitor %s (git: %s %s)\n", version, branch, commit)
	return nil
}