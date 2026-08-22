package main

import "fmt"

const rootHelp = `reqdb tracks requirements and their implementation in code.

Usage:
  reqdb [global options] COMMAND [arguments] [options]

Core Commands:
  requirement   Manage requirements and reconciliation
  review        Read immutable requirement reviews
  task          Manage implementation tasks
  lease         Maintain active task leases

Graph Commands:
  trace         Show a requirement hierarchy
  impact        Show requirements and tasks affected by a change
  render        Render requirements as Markdown

Other Commands:
  login         Save a server credential
  project       List, create, inspect, or select projects
  audit         List audit events
  serve         Start the API server
  version       Show build version information
  help          Show help for a command

Global Options:
  --server URL  API server URL (default "http://127.0.0.1:8080")
  --actor ID    Actor ID for the audit log (default "anonymous")
  --project ID  Project scope (or REQDB_PROJECT)
  --json        Print the API response as JSON
  -h, --help    Show help

Use "reqdb COMMAND --help" for more information about a command.
`

const requirementHelp = `Manage requirements and their reconciliation with code.

Usage:
  reqdb requirement ACTION [arguments] [options]

Actions:
  list       List requirements
  workable   List requirements that permit new work
  get        Show one requirement revision
  check      Validate a requirement file
  create     Create a requirement
  update     Create a requirement revision
  review     Accept or reject code for a requirement revision
  retire     Retire a requirement
  render     Render a requirement and its descendants

Use "reqdb requirement ACTION --help" for action options.
`

const taskHelp = `Manage tasks that implement or reconcile requirements.

Usage:
  reqdb task ACTION [arguments] [options]

Actions:
  list       List tasks
  workable   List tasks that are ready to lease
  get        Show one task
  create     Create a task from a file
  lease      Lease a task to an agent
  complete   Complete a leased task
  close      Close an open task without completing it
  link-pr    Link a pull request to a task

Use "reqdb task ACTION --help" for action options.
`

const reviewHelp = `Read immutable requirement reviews.

Usage:
  reqdb review ACTION [arguments] [options]

Actions:
  get    Show one review
  list   List reviews for one requirement

Use "reqdb review ACTION --help" for action options.
`

const leaseHelp = `Maintain a task lease.

Usage:
  reqdb lease ACTION [LEASE] [options]

Actions:
  list        List active leases
  heartbeat   Extend a lease
  release     Release a lease

Use "reqdb lease ACTION --help" for action options.
`

const loginHelp = `Save the shared bearer credential for a server.

Usage:
  reqdb login --server URL

The password is prompted without echo and saved per server in
~/.config/reqdb/token.json.
`

const projectHelp = `Manage project scopes.

Usage:
  reqdb project list [options]
  reqdb project get ID [options]
  reqdb project create ID [--name NAME] [--description TEXT] [options]
  reqdb project use ID [options]
`

const serveHelp = `Start the reqdb API server.

Usage:
  reqdb serve [options]

Options:
  --db PATH                     SQLite database path (default "reqdb.sqlite")
  --listen ADDRESS              Listen address (default "127.0.0.1:8080")
  --audit-retention-days DAYS   Audit retention period (default 90)
  -h, --help                    Show help

Environment:
  REQDB_PASSWORD                Required shared bearer password

Example:
  reqdb serve --db reqdb.sqlite --listen 127.0.0.1:8080
`

const clientOptionsHelp = `
Global Options:
  --server URL   API server URL (default "http://127.0.0.1:8080")
  --actor ID     Actor ID for the audit log (default "anonymous")
  --project ID   Project scope (or REQDB_PROJECT)
  --json         Print the API response as JSON
  -h, --help     Show help
`

var actionHelp = map[string]string{
	"review get": `Show one review.

Usage:
  reqdb review get REVIEW_ID [options]
`,
	"review list": `List reviews for one requirement.

Usage:
  reqdb review list REQUIREMENT_ID[@REVISION] [--cursor REVIEW_ID] [options]
`,
	"requirement list": `List requirements.

Usage:
  reqdb requirement list [options]

Options:
  --cursor ID       Continue after a requirement ID
  --level LEVEL     Filter by business, stakeholder, or system
  --state STATE     Filter by reconciliation state
  --json            Print the API response as JSON
`,
	"requirement workable": `List requirements that permit implementation or reconciliation work.

Usage:
  reqdb requirement workable [--cursor ID] [options]
`,
	"requirement get": `Show one requirement revision.

Usage:
  reqdb requirement get ID[@REVISION] [options]
`,
	"requirement check": `Validate a requirement without a database change.

Usage:
  reqdb requirement check ID --level LEVEL --title TITLE --statement STATEMENT [options]
  reqdb requirement check --from-file FILE [options]

Input Options:
  -f, --from-file FILE       Read YAML from FILE; use - for standard input
  --level LEVEL              business, stakeholder, or system
  --title TITLE              Requirement title
  --statement STATEMENT      One requirement obligation
  --refines ID@REVISION      Parent revision; repeat for more parents
  --depends-on ID@REVISION   Dependency revision; repeat for more dependencies
`,
	"requirement create": `Create a requirement.

Usage:
  reqdb requirement create ID --level LEVEL --title TITLE --statement STATEMENT [options]
  reqdb requirement create --from-file FILE [options]

Input Options:
  -f, --from-file FILE       Read YAML from FILE; use - for standard input
  --level LEVEL              business, stakeholder, or system
  --title TITLE              Requirement title
  --statement STATEMENT      One requirement obligation
  --refines ID@REVISION      Parent revision; repeat for more parents
  --depends-on ID@REVISION   Dependency revision; repeat for more dependencies
`,
	"requirement update": `Create the next requirement revision.

Usage:
  reqdb requirement update ID --expected REVISION --level LEVEL --title TITLE --statement STATEMENT [options]
  reqdb requirement update ID --from-file FILE --expected REVISION [options]

Input Options:
  -f, --from-file FILE       Read YAML from FILE; use - for standard input
  --level LEVEL              business, stakeholder, or system
  --title TITLE              Requirement title
  --statement STATEMENT      One requirement obligation
  --refines ID@REVISION      Parent revision; repeat for more parents
  --depends-on ID@REVISION   Dependency revision; repeat for more dependencies
`,
	"requirement review": `Review code against a requirement revision.

Usage:
  reqdb requirement review ID[@REVISION] --commit SHA --verdict VERDICT [options]
  reqdb requirement review ID[@REVISION] --from-file FILE [options]

Options:
  -f, --from-file FILE  Read YAML from FILE; use - for standard input
  --commit SHA          Full 40-character Git commit
  --verdict VERDICT     accept or reject
  --task ID             Completed task associated with the commit
  --finding MESSAGE     Rejection finding; repeat for more findings
`,
	"requirement retire": `Retire a requirement without deleting its history.

Usage:
  reqdb requirement retire ID [options]
`,
	"requirement render": `Render a requirement and its descendants as Markdown.

Usage:
  reqdb requirement render ID [options]
`,
	"task list": `List tasks.

Usage:
  reqdb task list [--cursor ID] [options]
`,
	"task workable": `List tasks that are ready to lease.

Usage:
  reqdb task workable [--cursor ID] [options]
`,
	"task get": `Show one task.

Usage:
  reqdb task get ID [options]
`,
	"task create": `Create a task.

Usage:
  reqdb task create ID --title TITLE --description TEXT --priority NUMBER --requirement LINK [options]
  reqdb task create --from-file FILE [options]

Input Options:
  -f, --from-file FILE       Read YAML from FILE; use - for standard input
  --title TITLE              Task title
  --description TEXT         Task description
  --priority NUMBER          Priority from 0 through 100
  --requirement LINK         REQUIREMENT@REVISION:PURPOSE; repeat for more links
  --depends-on TASK          Task dependency; repeat for more dependencies

PURPOSE is implement or reconcile.
`,
	"task lease": `Lease a workable task to an agent.

Usage:
  reqdb task lease ID --agent AGENT [--ttl DURATION] [options]
`,
	"task complete": `Complete a leased task at a Git commit.

Usage:
  reqdb task complete ID --lease LEASE --fence NUMBER --commit SHA [options]
`,
	"task close": `Close an open, unleased task without completing it.

Usage:
  reqdb task close ID [options]
`,
	"task link-pr": `Link a GitHub pull request to a task.

Usage:
  reqdb task link-pr ID --url URL [options]
`,
	"lease heartbeat": `Extend an active lease.

Usage:
  reqdb lease heartbeat LEASE --fence NUMBER [--ttl DURATION] [options]
`,
	"lease list": `List active leases.

Usage:
  reqdb lease list [options]

Options:
  --cursor ID   Continue after a lease ID
  --agent ID    Show leases held by one agent
  --task ID     Show the lease for one task
`,
	"lease release": `Release an active lease.

Usage:
  reqdb lease release LEASE --fence NUMBER [options]
`,
	"trace": `Show the requirement hierarchy.

Usage:
  reqdb trace [REQUIREMENT] [options]
`,
	"impact": `Show requirements and tasks affected by a change.

Usage:
  reqdb impact REQUIREMENT [options]
`,
	"audit": `List audit events.

Usage:
  reqdb audit [TYPE:ID] [options]
`,
	"render": `Render requirements as Markdown.

Usage:
  reqdb render [--root REQUIREMENT] [options]
`,
}

type commandError struct {
	message string
	help    string
}

func (err commandError) Error() string {
	return fmt.Sprintf("%s\n\n%s", err.message, err.help)
}

func withHelp(message, help string) error {
	if help != rootHelp && help != serveHelp {
		help += clientOptionsHelp
	}
	return commandError{message: message, help: help}
}

func helpFor(args []string) string {
	if len(args) == 0 {
		return rootHelp
	}
	if len(args) >= 2 {
		if help, ok := actionHelp[args[0]+" "+args[1]]; ok {
			return help
		}
	}
	switch args[0] {
	case "requirement":
		return requirementHelp
	case "review":
		return reviewHelp
	case "task":
		return taskHelp
	case "lease":
		return leaseHelp
	case "login":
		return loginHelp
	case "project":
		return projectHelp
	case "serve":
		return serveHelp
	default:
		if help, ok := actionHelp[args[0]]; ok {
			return help
		}
		return rootHelp
	}
}

func isHelpRequest(args []string) bool {
	if len(args) == 0 || args[0] == "help" {
		return true
	}
	if len(args) == 1 && (args[0] == "requirement" || args[0] == "review" || args[0] == "task" || args[0] == "lease" || args[0] == "login" || args[0] == "project") {
		return true
	}
	return has(args, "-h") || has(args, "--help")
}

func printHelp(args []string) {
	help := helpFor(args)
	fmt.Print(help)
	if help != rootHelp && help != serveHelp {
		fmt.Print(clientOptionsHelp)
	}
}
