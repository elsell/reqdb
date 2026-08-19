package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/elsell/reqdb/internal/application"
	"github.com/elsell/reqdb/internal/client"
	"github.com/elsell/reqdb/internal/domain"
	"github.com/elsell/reqdb/internal/observability"
	"github.com/elsell/reqdb/internal/store/sqlite"
	"github.com/elsell/reqdb/internal/transport/httpapi"
	"github.com/elsell/reqdb/internal/transport/webui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var apiError client.APIError
		if errors.As(err, &apiError) {
			fmt.Fprintf(os.Stderr, "Error: %s.\n", sentence(apiError.Message))
			fmt.Fprintf(os.Stderr, "Correlation ID: %s\n", apiError.CorrelationID)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	args = normalizeGlobalArgs(args)
	if isHelpRequest(args) {
		if len(args) > 0 && args[0] == "help" {
			args = args[1:]
		}
		printHelp(args)
		return nil
	}
	if args[0] == "serve" {
		return serve(args[1:])
	}
	known := map[string]bool{"requirement": true, "task": true, "lease": true, "trace": true, "impact": true, "audit": true, "render": true}
	if !known[args[0]] {
		return withHelp(fmt.Sprintf("unknown command %q", args[0]), rootHelp)
	}
	server := option(args, "--server")
	if server == "" {
		server = os.Getenv("REQDB_SERVER")
	}
	if server == "" {
		server = "http://127.0.0.1:8080"
	}
	actor := option(args, "--actor")
	if actor == "" {
		actor = "anonymous"
	}
	api := client.Client{BaseURL: server, ActorID: actor, HTTP: &http.Client{Timeout: 30 * time.Second}}
	jsonOutput := has(args, "--json")
	ctx := context.Background()
	switch args[0] {
	case "requirement":
		return requirement(ctx, api, args[1:], jsonOutput)
	case "task":
		return task(ctx, api, args[1:], jsonOutput)
	case "lease":
		return lease(ctx, api, args[1:], jsonOutput)
	case "trace":
		root := ""
		if len(args) > 1 && !strings.HasPrefix(args[1], "--") {
			root = args[1]
		}
		return call(ctx, api, http.MethodGet, "/v1/trace/"+url.PathEscape(root), nil, jsonOutput)
	case "impact":
		if len(args) < 2 {
			return withHelp("a requirement ID is required", actionHelp["impact"])
		}
		return call(ctx, api, http.MethodGet, "/v1/impact/"+url.PathEscape(args[1]), nil, jsonOutput)
	case "audit":
		entity := ""
		if len(args) > 1 && !strings.HasPrefix(args[1], "--") {
			entity = args[1]
		}
		return call(ctx, api, http.MethodGet, "/v1/audit?entity="+url.QueryEscape(entity), nil, jsonOutput)
	case "render":
		value, err := api.Render(ctx, "/v1/render?root="+url.QueryEscape(option(args, "--root")))
		if err == nil {
			fmt.Print(value)
		}
		return err
	default:
		return withHelp(fmt.Sprintf("unknown command %q", args[0]), rootHelp)
	}
}

func serve(args []string) error {
	set := flag.NewFlagSet("serve", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	dbPath := set.String("db", "reqdb.sqlite", "SQLite database path")
	listen := set.String("listen", "127.0.0.1:8080", "listen address")
	retention := set.Int("audit-retention-days", 90, "audit retention in days")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Print(serveHelp)
			return nil
		}
		return withHelp(err.Error(), serveHelp)
	}
	if *retention < 1 {
		return withHelp("audit retention must be positive", serveHelp)
	}
	store, err := sqlite.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	broker := observability.NewBroker()
	events := observability.Fanout{observability.LogSink{Logger: logger}, observability.OTelSink{}, broker}
	service := application.Service{Store: store, Auth: application.AllowAll{}, Events: events}
	mux := http.NewServeMux()
	mux.Handle("/v1/", httpapi.API{Service: service, Events: broker}.Handler())
	mux.Handle("/", webui.Handler())
	handler := mux
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	server.RegisterOnShutdown(broker.Close)
	prune := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := store.PruneAudit(ctx, time.Now().UTC().AddDate(0, 0, -*retention)); err != nil {
			logger.Error("audit prune failed", "error", err)
		}
	}
	prune()
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				prune()
			case <-stop:
				return
			}
		}
	}()
	errorsChannel := make(chan error, 1)
	go func() { logger.Info("server started", "address", *listen); errorsChannel <- server.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-signals:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		close(stop)
		return server.Shutdown(ctx)
	case err := <-errorsChannel:
		close(stop)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func requirement(ctx context.Context, api client.Client, args []string, jsonOutput bool) error {
	if len(args) == 0 {
		return withHelp("a requirement action is required", requirementHelp)
	}
	action := args[0]
	switch action {
	case "list", "ready":
		path := "/v1/requirements?cursor=" + url.QueryEscape(option(args, "--cursor"))
		if action == "ready" {
			path += "&ready=true"
		} else {
			path += "&level=" + url.QueryEscape(option(args, "--level")) + "&state=" + url.QueryEscape(option(args, "--state"))
		}
		return call(ctx, api, http.MethodGet, path, nil, jsonOutput)
	case "get":
		if len(args) < 2 {
			return withHelp("a requirement ID is required", actionHelp["requirement get"])
		}
		return call(ctx, api, http.MethodGet, "/v1/requirements/"+url.PathEscape(args[1]), nil, jsonOutput)
	case "check", "create":
		if option(args, "--from-file") == "" {
			return withHelp("--from-file is required", actionHelp["requirement "+action])
		}
		input, err := readRequirement(option(args, "--from-file"))
		if err != nil {
			return err
		}
		path := "/v1/requirements"
		if action == "check" {
			path += "/check"
		}
		return call(ctx, api, http.MethodPost, path, input, jsonOutput)
	case "update":
		if len(args) < 2 {
			return withHelp("a requirement ID is required", actionHelp["requirement update"])
		}
		if option(args, "--from-file") == "" {
			return withHelp("--from-file is required", actionHelp["requirement update"])
		}
		input, err := readRequirement(option(args, "--from-file"))
		if err != nil {
			return err
		}
		expected, err := requiredInt(args, "--expected")
		if err != nil {
			return withHelp(err.Error(), actionHelp["requirement update"])
		}
		return call(ctx, api, http.MethodPut, "/v1/requirements/"+url.PathEscape(args[1])+"?expected="+strconv.Itoa(expected), input, jsonOutput)
	case "confirm":
		if len(args) < 2 {
			return withHelp("a requirement ID is required", actionHelp["requirement confirm"])
		}
		if option(args, "--commit") == "" {
			return withHelp("--commit is required", actionHelp["requirement confirm"])
		}
		body := map[string]any{"commit": option(args, "--commit"), "result": option(args, "--result")}
		return call(ctx, api, http.MethodPost, "/v1/requirements/"+url.PathEscape(args[1])+"/confirm", body, jsonOutput)
	case "retire":
		if len(args) < 2 {
			return withHelp("a requirement ID is required", actionHelp["requirement retire"])
		}
		return call(ctx, api, http.MethodPost, "/v1/requirements/"+url.PathEscape(args[1])+"/retire", nil, jsonOutput)
	case "render":
		if len(args) < 2 {
			return withHelp("a requirement ID is required", actionHelp["requirement render"])
		}
		value, err := api.Render(ctx, "/v1/render?root="+url.QueryEscape(args[1]))
		if err == nil {
			fmt.Print(value)
		}
		return err
	}
	return withHelp(fmt.Sprintf("unknown requirement action %q", action), requirementHelp)
}

func task(ctx context.Context, api client.Client, args []string, jsonOutput bool) error {
	if len(args) == 0 {
		return withHelp("a task action is required", taskHelp)
	}
	action := args[0]
	switch action {
	case "list", "ready":
		path := "/v1/tasks?cursor=" + url.QueryEscape(option(args, "--cursor"))
		if action == "ready" {
			path += "&ready=true"
		}
		return call(ctx, api, http.MethodGet, path, nil, jsonOutput)
	case "get":
		if len(args) < 2 {
			return withHelp("a task ID is required", actionHelp["task get"])
		}
		return call(ctx, api, http.MethodGet, "/v1/tasks/"+url.PathEscape(args[1]), nil, jsonOutput)
	case "create":
		if option(args, "--from-file") == "" {
			return withHelp("--from-file is required", actionHelp["task create"])
		}
		input, err := readTask(option(args, "--from-file"))
		if err != nil {
			return err
		}
		return call(ctx, api, http.MethodPost, "/v1/tasks", input, jsonOutput)
	case "lease":
		if len(args) < 2 {
			return withHelp("a task ID is required", actionHelp["task lease"])
		}
		if option(args, "--agent") == "" {
			return withHelp("--agent is required", actionHelp["task lease"])
		}
		return call(ctx, api, http.MethodPost, "/v1/tasks/"+url.PathEscape(args[1])+"/lease", map[string]any{"agent": option(args, "--agent"), "ttl": option(args, "--ttl")}, jsonOutput)
	case "complete":
		if len(args) < 2 {
			return withHelp("a task ID is required", actionHelp["task complete"])
		}
		if option(args, "--lease") == "" || option(args, "--commit") == "" {
			return withHelp("--lease and --commit are required", actionHelp["task complete"])
		}
		fence, err := requiredInt(args, "--fence")
		if err != nil {
			return withHelp(err.Error(), actionHelp["task complete"])
		}
		return call(ctx, api, http.MethodPost, "/v1/tasks/"+url.PathEscape(args[1])+"/complete", map[string]any{"lease": option(args, "--lease"), "fence": fence, "commit": option(args, "--commit")}, jsonOutput)
	case "link-pr":
		if len(args) < 2 {
			return withHelp("a task ID is required", actionHelp["task link-pr"])
		}
		if option(args, "--url") == "" {
			return withHelp("--url is required", actionHelp["task link-pr"])
		}
		pr, err := httpapi.ParsePullRequest(option(args, "--url"))
		if err != nil {
			return err
		}
		return call(ctx, api, http.MethodPost, "/v1/tasks/"+url.PathEscape(args[1])+"/pull-requests", pr, jsonOutput)
	}
	return withHelp(fmt.Sprintf("unknown task action %q", action), taskHelp)
}

func lease(ctx context.Context, api client.Client, args []string, jsonOutput bool) error {
	if len(args) == 0 {
		return withHelp("a lease action is required", leaseHelp)
	}
	if args[0] == "list" {
		path := "/v1/leases?cursor=" + url.QueryEscape(option(args, "--cursor")) + "&agent=" + url.QueryEscape(option(args, "--agent")) + "&task=" + url.QueryEscape(option(args, "--task"))
		return call(ctx, api, http.MethodGet, path, nil, jsonOutput)
	}
	if len(args) < 2 {
		return withHelp("a lease ID is required", leaseHelp)
	}
	action, id := args[0], args[1]
	fence, err := requiredInt(args, "--fence")
	if err != nil {
		help := leaseHelp
		if value, ok := actionHelp["lease "+action]; ok {
			help = value
		}
		return withHelp(err.Error(), help)
	}
	if action != "heartbeat" && action != "release" {
		return withHelp(fmt.Sprintf("unknown lease action %q", action), leaseHelp)
	}
	return call(ctx, api, http.MethodPost, "/v1/leases/"+url.PathEscape(id)+"/"+action, map[string]any{"fence": fence, "ttl": option(args, "--ttl")}, jsonOutput)
}

func call(ctx context.Context, api client.Client, method, path string, body any, jsonOutput bool) error {
	envelope, err := api.Do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if jsonOutput {
		value, _ := json.MarshalIndent(envelope, "", "  ")
		fmt.Println(string(value))
		return nil
	}
	if err := printHuman(method, path, envelope.Data); err != nil {
		return err
	}
	if envelope.Meta.NextCursor != "" {
		fmt.Fprintln(os.Stderr, "next cursor:", envelope.Meta.NextCursor)
	}
	return nil
}

func sentence(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(value, "."))
	if value == "" {
		return "The request failed"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
func readRequirement(path string) (domain.RequirementInput, error) {
	file, err := os.Open(path)
	if err != nil {
		return domain.RequirementInput{}, err
	}
	defer file.Close()
	return application.DecodeRequirement(file)
}
func readTask(path string) (domain.TaskInput, error) {
	file, err := os.Open(path)
	if err != nil {
		return domain.TaskInput{}, err
	}
	defer file.Close()
	return application.DecodeTask(file)
}
func option(args []string, name string) string {
	for i, value := range args {
		if value == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(value, name+"=") {
			return strings.TrimPrefix(value, name+"=")
		}
	}
	return ""
}
func has(args []string, name string) bool {
	for _, value := range args {
		if value == name {
			return true
		}
	}
	return false
}
func normalizeGlobalArgs(args []string) []string {
	positionals := make([]string, 0, len(args))
	globals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		value := args[i]
		switch {
		case value == "--json":
			globals = append(globals, value)
		case value == "--server" || value == "--actor":
			globals = append(globals, value)
			if i+1 < len(args) {
				i++
				globals = append(globals, args[i])
			}
		case strings.HasPrefix(value, "--server=") || strings.HasPrefix(value, "--actor="):
			globals = append(globals, value)
		default:
			positionals = append(positionals, value)
		}
	}
	return append(positionals, globals...)
}
func requiredInt(args []string, name string) (int, error) {
	value := option(args, name)
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return number, nil
}
