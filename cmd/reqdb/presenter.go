package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/elsell/reqdb/internal/domain"
)

func printHuman(method, rawPath string, data json.RawMessage) error {
	parsed, err := url.Parse(rawPath)
	if err != nil {
		return err
	}
	path := strings.Trim(parsed.Path, "/")
	parts := strings.Split(path, "/")

	switch {
	case path == "v1/requirements/check":
		fmt.Println("Requirement file is valid.")
		return nil
	case path == "v1/requirements" && method == http.MethodGet:
		return printRequirementList(data)
	case parts[0] == "v1" && len(parts) >= 2 && parts[1] == "requirements":
		return printRequirement(data)
	case path == "v1/tasks" && method == http.MethodGet:
		return printTaskList(data)
	case len(parts) >= 3 && parts[1] == "tasks" && parts[len(parts)-1] == "lease":
		return printLease(data)
	case len(parts) >= 3 && parts[1] == "tasks" && parts[len(parts)-1] == "pull-requests":
		fmt.Println("Pull request linked.")
		return nil
	case len(parts) >= 2 && parts[1] == "tasks":
		return printTask(data)
	case len(parts) >= 2 && parts[1] == "leases" && parts[len(parts)-1] == "release":
		fmt.Println("Lease released.")
		return nil
	case path == "v1/leases" && method == http.MethodGet:
		return printLeaseList(data)
	case len(parts) >= 2 && parts[1] == "leases":
		return printLease(data)
	case len(parts) >= 2 && parts[1] == "audit":
		return printAudit(data)
	case len(parts) >= 2 && parts[1] == "trace":
		return printRequirementTree(data)
	case len(parts) >= 2 && parts[1] == "impact":
		return printImpact(data)
	default:
		return fmt.Errorf("no human output format for %s %s", method, parsed.Path)
	}
}

func printRequirementList(data json.RawMessage) error {
	var items []domain.Requirement
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("No requirements found.")
		return nil
	}
	table := newTable()
	fmt.Fprintln(table, "ID\tREVISION\tLEVEL\tLIFECYCLE\tRECONCILIATION\tTITLE")
	for _, item := range items {
		fmt.Fprintf(table, "%s\t%d\t%s\t%s\t%s\t%s\n", item.ID, item.CurrentRevision, item.Revision.Level, item.LifecycleState, item.ReconciliationState, item.Revision.Title)
	}
	return table.Flush()
}

func printRequirement(data json.RawMessage) error {
	var item domain.Requirement
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	fmt.Printf("Requirement %s@%d\n\n", item.ID, item.Revision.Revision)
	fields := newTable()
	fmt.Fprintf(fields, "Title:\t%s\n", item.Revision.Title)
	fmt.Fprintf(fields, "Level:\t%s\n", item.Revision.Level)
	fmt.Fprintf(fields, "Lifecycle:\t%s\n", item.LifecycleState)
	fmt.Fprintf(fields, "Reconciliation:\t%s\n", item.ReconciliationState)
	fmt.Fprintf(fields, "Refines:\t%s\n", parentList(item.Revision.Parents))
	fmt.Fprintf(fields, "Depends on:\t%s\n", parentList(item.Revision.Dependencies))
	fmt.Fprintf(fields, "Created:\t%s\n", displayTime(item.Revision.CreatedAt))
	fmt.Fprintf(fields, "Actor:\t%s\n", item.Revision.ActorID)
	if err := fields.Flush(); err != nil {
		return err
	}
	fmt.Printf("\nStatement:\n  %s\n", item.Revision.Statement)
	printReadiness(item.Readiness)
	if len(item.OpenCauses) > 0 {
		fmt.Println("\nOpen reconciliation causes:")
		for _, cause := range item.OpenCauses {
			fmt.Printf("  %s@%d  %s\n", cause.Requirement.ID, cause.Requirement.Revision, displayTime(cause.CreatedAt))
		}
	}
	if len(item.RevisionHistory) > 0 {
		fmt.Println("\nRevision history:")
		table := newTable()
		fmt.Fprintln(table, "REVISION\tCREATED\tACTOR\tTITLE")
		for _, revision := range item.RevisionHistory {
			fmt.Fprintf(table, "%d\t%s\t%s\t%s\n", revision.Revision, displayTime(revision.CreatedAt), revision.ActorID, revision.Title)
		}
		_ = table.Flush()
	}
	printStateHistory(item.StateHistory)
	if len(item.Confirmations) > 0 {
		fmt.Println("\nConfirmation history:")
		table := newTable()
		fmt.Fprintln(table, "TIME\tRESULT\tCOMMIT\tTASK\tPULL REQUEST\tACTOR")
		for _, confirmation := range item.Confirmations {
			pr := ""
			if confirmation.PullRequest != nil {
				pr = confirmation.PullRequest.URL
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", displayTime(confirmation.ConfirmedAt), confirmation.Result, confirmation.Commit, confirmation.TaskID, pr, confirmation.ActorID)
		}
		_ = table.Flush()
	}
	return nil
}

func printRequirementTree(data json.RawMessage) error {
	return printRequirementTreeWithColor(data, terminalColorEnabled())
}

func printRequirementTreeWithColor(data json.RawMessage, color bool) error {
	var graph domain.RequirementGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return err
	}
	items := graph.Requirements
	if len(items) == 0 {
		fmt.Println("No requirements found.")
		return nil
	}
	byID := make(map[string]domain.Requirement, len(items))
	children := make(map[string][]domain.Requirement)
	for _, item := range items {
		byID[item.ID] = item
	}
	rootIDs := make(map[string]bool, len(items))
	for _, item := range items {
		rootIDs[item.ID] = true
		for _, parent := range item.Revision.Parents {
			if _, ok := byID[parent.ID]; ok {
				children[parent.ID] = append(children[parent.ID], item)
				rootIDs[item.ID] = false
			}
		}
	}
	less := func(items []domain.Requirement) {
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	}
	for id := range children {
		less(children[id])
	}
	tasks := make(map[string][]domain.Task)
	for _, task := range graph.Tasks {
		for _, link := range task.Requirements {
			tasks[link.Requirement] = append(tasks[link.Requirement], task)
		}
	}
	for ref := range tasks {
		sort.Slice(tasks[ref], func(i, j int) bool { return tasks[ref][i].ID < tasks[ref][j].ID })
	}
	roots := make([]domain.Requirement, 0)
	for _, item := range items {
		if rootIDs[item.ID] {
			roots = append(roots, item)
		}
	}
	less(roots)
	widths := traceColumnWidths(items, graph.Tasks)
	fmt.Printf("%-*s  %-*s  %-*s  %s\n", widths.requirement, "REQUIREMENT", widths.level, "LEVEL", widths.state, "STATE", "TITLE")
	expanded := map[string]bool{}
	for _, root := range roots {
		printTreeNode(os.Stdout, root, "", true, true, children, tasks, map[string]bool{}, expanded, widths, color)
	}
	hasDependencies := false
	for _, item := range items {
		hasDependencies = hasDependencies || len(item.Revision.Dependencies) > 0
	}
	if hasDependencies {
		fmt.Println("\nDependency links:")
		links := newTable()
		fmt.Fprintln(links, "REQUIREMENT\tDEPENDS ON")
		for _, item := range items {
			for _, dependency := range item.Revision.Dependencies {
				fmt.Fprintf(links, "%s@%d\t%s@%d\n", item.ID, item.Revision.Revision, dependency.ID, dependency.Revision)
			}
		}
		return links.Flush()
	}
	return nil
}

type traceWidths struct {
	requirement int
	level       int
	state       int
}

func traceColumnWidths(requirements []domain.Requirement, tasks []domain.Task) traceWidths {
	widths := traceWidths{requirement: len("REQUIREMENT"), level: len("LEVEL"), state: len("STATE")}
	for _, item := range requirements {
		widths.requirement = max(widths.requirement, utf8.RuneCountInString(item.ID)+len(fmt.Sprintf("@%d", item.Revision.Revision))+16)
		widths.level = max(widths.level, utf8.RuneCountInString(item.Revision.Level)+2)
		state := string(item.ReconciliationState)
		if item.LifecycleState == domain.Retired {
			state = string(domain.Retired)
		}
		widths.state = max(widths.state, utf8.RuneCountInString(state)+2)
	}
	for _, task := range tasks {
		widths.requirement = max(widths.requirement, utf8.RuneCountInString(task.ID)+16)
		widths.level = max(widths.level, len("task")+2)
		widths.state = max(widths.state, utf8.RuneCountInString(task.State)+2)
	}
	return widths
}

func printTreeNode(writer io.Writer, item domain.Requirement, prefix string, last, root bool, children map[string][]domain.Requirement, tasks map[string][]domain.Task, path, expanded map[string]bool, widths traceWidths, color bool) {
	branch := ""
	if root {
		branch = "● "
	} else {
		if last {
			branch = "└── "
		} else {
			branch = "├── "
		}
	}
	state := string(item.ReconciliationState)
	if item.LifecycleState == domain.Retired {
		state = string(domain.Retired)
	}
	title := item.Revision.Title
	if expanded[item.ID] {
		title += " (reference; expanded above)"
	}
	version := fmt.Sprintf("@%d", item.Revision.Revision)
	firstWidth := utf8.RuneCountInString(prefix + branch + item.ID + version)
	fmt.Fprint(writer, ansi(prefix+branch, "90", color), ansi(item.ID, "1", color), ansi(version, "90", color))
	fmt.Fprint(writer, strings.Repeat(" ", widths.requirement-firstWidth+2))
	level := traceBadge(item.Revision.Level, traceLevelColors[item.Revision.Level], color)
	fmt.Fprint(writer, level, strings.Repeat(" ", widths.level-(utf8.RuneCountInString(item.Revision.Level)+2)+2))
	stateBadge := traceBadge(state, traceStateColors[state], color)
	fmt.Fprint(writer, stateBadge, strings.Repeat(" ", widths.state-(utf8.RuneCountInString(state)+2)+2), title, "\n")
	if path[item.ID] || expanded[item.ID] {
		return
	}
	expanded[item.ID] = true
	nextPath := make(map[string]bool, len(path)+1)
	for id, value := range path {
		nextPath[id] = value
	}
	nextPath[item.ID] = true
	nextPrefix := prefix
	if !root {
		if last {
			nextPrefix += "    "
		} else {
			nextPrefix += "│   "
		}
	}
	requirementChildren := children[item.ID]
	taskChildren := tasks[fmt.Sprintf("%s@%d", item.ID, item.Revision.Revision)]
	for index, child := range requirementChildren {
		isLast := index == len(requirementChildren)-1 && len(taskChildren) == 0
		printTreeNode(writer, child, nextPrefix, isLast, false, children, tasks, nextPath, expanded, widths, color)
	}
	for index, task := range taskChildren {
		branch := "├── "
		if index == len(taskChildren)-1 {
			branch = "└── "
		}
		firstWidth := utf8.RuneCountInString(nextPrefix + branch + task.ID)
		fmt.Fprint(writer, ansi(nextPrefix+branch, "90", color), task.ID, strings.Repeat(" ", widths.requirement-firstWidth+2))
		level := traceBadge("task", "97;100", color)
		fmt.Fprint(writer, level, strings.Repeat(" ", widths.level-(len("task")+2)+2))
		stateBadge := traceBadge(task.State, traceStateColors[task.State], color)
		fmt.Fprint(writer, stateBadge, strings.Repeat(" ", widths.state-(utf8.RuneCountInString(task.State)+2)+2), task.Title, "\n")
	}
}

var traceLevelColors = map[string]string{
	"business":    "97;45",
	"stakeholder": "97;44",
	"system":      "30;46",
	"software":    "30;42",
}

var traceStateColors = map[string]string{
	"implemented":          "30;42",
	"unimplemented":        "97;100",
	"in_progress":          "97;44",
	"ready_for_review":     "30;43",
	"needs_reconciliation": "97;41",
	"retired":              "97;100",
	"open":                 "97;44",
	"complete":             "30;42",
	"closed":               "97;100",
}

func traceBadge(value, colors string, enabled bool) string {
	if colors == "" {
		colors = "97;100"
	}
	return ansi(" "+value+" ", colors, enabled)
}

func ansi(value, codes string, enabled bool) string {
	if !enabled {
		return value
	}
	return "\x1b[" + codes + "m" + value + "\x1b[0m"
}

func terminalColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func printImpact(data json.RawMessage) error {
	var graph domain.RequirementGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return err
	}
	fmt.Println("Affected requirements:")
	requirements, _ := json.Marshal(graph.Requirements)
	if err := printRequirementList(requirements); err != nil {
		return err
	}
	fmt.Println("\nAffected tasks:")
	tasks, _ := json.Marshal(graph.Tasks)
	return printTaskList(tasks)
}

func printTaskList(data json.RawMessage) error {
	var items []domain.Task
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("No tasks found.")
		return nil
	}
	table := newTable()
	fmt.Fprintln(table, "ID\tSTATE\tPRIORITY\tREQUIREMENTS\tPRS\tTITLE")
	for _, item := range items {
		fmt.Fprintf(table, "%s\t%s\t%d\t%d\t%d\t%s\n", item.ID, item.State, item.Priority, len(item.Requirements), len(item.PullRequests), item.Title)
	}
	return table.Flush()
}

func printTask(data json.RawMessage) error {
	var item domain.Task
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	fmt.Printf("Task %s\n\n", item.ID)
	fields := newTable()
	fmt.Fprintf(fields, "Title:\t%s\n", item.Title)
	fmt.Fprintf(fields, "State:\t%s\n", item.State)
	fmt.Fprintf(fields, "Priority:\t%d\n", item.Priority)
	fmt.Fprintf(fields, "Version:\t%d\n", item.Version)
	fmt.Fprintf(fields, "Depends on:\t%s\n", valueList(item.DependsOn))
	if item.CompletedCommit != "" {
		fmt.Fprintf(fields, "Commit:\t%s\n", item.CompletedCommit)
	}
	if err := fields.Flush(); err != nil {
		return err
	}
	fmt.Printf("\nDescription:\n  %s\n", item.Description)
	if len(item.Requirements) > 0 {
		fmt.Println("\nRequirements:")
		for _, link := range item.Requirements {
			fmt.Printf("  %s  (%s)\n", link.Requirement, link.Purpose)
		}
	}
	if len(item.PullRequests) > 0 {
		fmt.Println("\nPull requests:")
		for _, pr := range item.PullRequests {
			fmt.Printf("  %s\n", pr.URL)
		}
	}
	printReadiness(item.Readiness)
	printStateHistory(item.StateHistory)
	return nil
}

func printReadiness(readiness *domain.Readiness) {
	if readiness == nil {
		return
	}
	value := "ready"
	if !readiness.Ready {
		value = "blocked"
	}
	fmt.Printf("\nReadiness: %s\n", value)
	for _, blocker := range readiness.Blockers {
		fmt.Printf("  - %s\n", blocker)
	}
}

func printStateHistory(items []domain.StateChange) {
	if len(items) == 0 {
		return
	}
	fmt.Println("\nState history:")
	table := newTable()
	fmt.Fprintln(table, "TIME\tFIELD\tFROM\tTO\tACTOR")
	for _, item := range items {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", displayTime(item.OccurredAt), item.Field, item.From, item.To, item.ActorID)
	}
	_ = table.Flush()
}

func printLease(data json.RawMessage) error {
	var item domain.Lease
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	fmt.Printf("Lease %s\n\n", item.LeaseID)
	fields := newTable()
	fmt.Fprintf(fields, "Task:\t%s\n", item.TaskID)
	fmt.Fprintf(fields, "Agent:\t%s\n", item.AgentID)
	fmt.Fprintf(fields, "Fence:\t%d\n", item.Fence)
	fmt.Fprintf(fields, "Claimed:\t%s\n", displayTime(item.ClaimedAt))
	fmt.Fprintf(fields, "Expires:\t%s\n", displayTime(item.ExpiresAt))
	return fields.Flush()
}

func printLeaseList(data json.RawMessage) error {
	var items []domain.Lease
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("No active leases found.")
		return nil
	}
	table := newTable()
	fmt.Fprintln(table, "LEASE\tTASK\tAGENT\tFENCE\tCLAIMED\tEXPIRES")
	for _, item := range items {
		fmt.Fprintf(table, "%s\t%s\t%s\t%d\t%s\t%s\n", item.LeaseID, item.TaskID, item.AgentID, item.Fence, displayTime(item.ClaimedAt), displayTime(item.ExpiresAt))
	}
	return table.Flush()
}

func printAudit(data json.RawMessage) error {
	var items []domain.AuditEvent
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("No audit events found.")
		return nil
	}
	table := newTable()
	fmt.Fprintln(table, "SEQUENCE\tTIME\tACTOR\tEVENT\tENTITY")
	for _, item := range items {
		fmt.Fprintf(table, "%d\t%s\t%s\t%s\t%s:%s\n", item.Sequence, displayTime(item.OccurredAt), item.ActorID, item.Kind, item.EntityType, item.EntityID)
	}
	return table.Flush()
}

func newTable() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
}

func parentList(items []domain.RequirementRef) string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, fmt.Sprintf("%s@%d", item.ID, item.Revision))
	}
	return valueList(values)
}

func valueList(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	sort.Strings(items)
	return strings.Join(items, ", ")
}

func displayTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
}
