package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/elsell/reqdb/internal/application"
	"github.com/elsell/reqdb/internal/domain"
)

type API struct{ Service application.Service }
type envelope struct {
	Data  any       `json:"data,omitempty"`
	Error *apiError `json:"error,omitempty"`
	Meta  meta      `json:"meta"`
}
type apiError struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlation_id"`
}
type meta struct {
	CorrelationID string `json:"correlation_id"`
	NextCursor    string `json:"next_cursor,omitempty"`
}

func (api API) Handler() http.Handler { return api.middleware(http.HandlerFunc(api.route)) }
func (api API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlation := r.Header.Get("X-Correlation-ID")
		if correlation == "" {
			correlation = application.NewID()
		}
		causation := r.Header.Get("X-Causation-ID")
		if causation == "" {
			causation = correlation
		}
		w.Header().Set("X-Correlation-ID", correlation)
		ctx := application.WithRequestIDs(r.Context(), correlation, causation)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func actor(r *http.Request) string {
	value := r.Header.Get("X-Actor-ID")
	if value == "" {
		return "anonymous"
	}
	return value
}
func write(w http.ResponseWriter, r *http.Request, status int, data any, cursor string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Data: data, Meta: meta{CorrelationID: application.CorrelationID(r.Context()), NextCursor: cursor}})
}
func fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest
	code := "INVALID_REQUEST"
	if errors.Is(err, domain.ErrNotFound) {
		status = http.StatusNotFound
		code = "NOT_FOUND"
	} else if errors.Is(err, domain.ErrConflict) {
		status = http.StatusConflict
		code = "CONFLICT"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Error: &apiError{Code: code, Message: err.Error(), CorrelationID: application.CorrelationID(r.Context())}, Meta: meta{CorrelationID: application.CorrelationID(r.Context())}})
}
func decode(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
func limit(r *http.Request) int {
	value, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if value < 1 {
		return 50
	}
	if value > 200 {
		return 200
	}
	return value
}

func (api API) route(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		fail(w, r, errors.New("API path must start with /v1"))
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/"), "/")
	parts := strings.Split(path, "/")
	switch parts[0] {
	case "requirements":
		api.requirements(w, r, parts[1:])
	case "tasks":
		api.tasks(w, r, parts[1:])
	case "leases":
		api.leases(w, r, parts[1:])
	case "trace":
		api.trace(w, r, parts[1:])
	case "impact":
		api.impact(w, r, parts[1:])
	case "audit":
		api.audit(w, r)
	case "render":
		api.render(w, r)
	default:
		fail(w, r, domain.ErrNotFound)
	}
}

func (api API) requirements(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 1 && parts[0] == "check" && r.Method == http.MethodPost {
		var input domain.RequirementInput
		if err := decode(r, &input); err != nil {
			fail(w, r, err)
			return
		}
		if err := input.Validate(); err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, map[string]bool{"valid": true}, "")
		return
	}
	if len(parts) == 0 && r.Method == http.MethodGet {
		page, err := api.Service.ListRequirements(r.Context(), r.URL.Query().Get("cursor"), limit(r), r.URL.Query().Get("level"), r.URL.Query().Get("state"), actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, page.Items, page.NextCursor)
		return
	}
	if len(parts) == 0 && r.Method == http.MethodPost {
		var input domain.RequirementInput
		if err := decode(r, &input); err != nil {
			fail(w, r, err)
			return
		}
		item, err := api.Service.CreateRequirement(r.Context(), input, actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 201, item, "")
		return
	}
	if len(parts) < 1 {
		fail(w, r, domain.ErrNotFound)
		return
	}
	ref, err := domain.ParseRequirementRef(parts[0])
	if !strings.Contains(parts[0], "@") {
		ref = domain.RequirementRef{ID: parts[0]}
		err = nil
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		item, err := api.Service.GetRequirement(r.Context(), ref, actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, item, "")
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPut {
		expected, _ := strconv.Atoi(r.URL.Query().Get("expected"))
		var input domain.RequirementInput
		if err := decode(r, &input); err != nil {
			fail(w, r, err)
			return
		}
		if input.ID != ref.ID {
			fail(w, r, errors.New("path and body requirement IDs differ"))
			return
		}
		item, err := api.Service.UpdateRequirement(r.Context(), input, expected, actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, item, "")
		return
	}
	if len(parts) == 2 && parts[1] == "confirm" && r.Method == http.MethodPost {
		var body struct {
			Commit string `json:"commit"`
			Result string `json:"result"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, r, err)
			return
		}
		item, err := api.Service.ConfirmRequirement(r.Context(), ref, body.Commit, body.Result, actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, item, "")
		return
	}
	fail(w, r, domain.ErrNotFound)
}

func (api API) tasks(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 && r.Method == http.MethodGet {
		page, err := api.Service.ListTasks(r.Context(), r.URL.Query().Get("cursor"), limit(r), r.URL.Query().Get("ready") == "true", actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, page.Items, page.NextCursor)
		return
	}
	if len(parts) == 0 && r.Method == http.MethodPost {
		var input domain.TaskInput
		if err := decode(r, &input); err != nil {
			fail(w, r, err)
			return
		}
		item, err := api.Service.CreateTask(r.Context(), input, actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 201, item, "")
		return
	}
	if len(parts) < 1 {
		fail(w, r, domain.ErrNotFound)
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		item, err := api.Service.GetTask(r.Context(), id, actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, item, "")
		return
	}
	if len(parts) == 2 && parts[1] == "lease" && r.Method == http.MethodPost {
		var body struct {
			Agent string `json:"agent"`
			TTL   string `json:"ttl"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, r, err)
			return
		}
		ttl := 30 * time.Minute
		if body.TTL != "" {
			var err error
			ttl, err = time.ParseDuration(body.TTL)
			if err != nil {
				fail(w, r, err)
				return
			}
		}
		item, err := api.Service.LeaseTask(r.Context(), id, body.Agent, ttl, actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, item, "")
		return
	}
	if len(parts) == 2 && parts[1] == "complete" && r.Method == http.MethodPost {
		var body struct {
			Lease  string `json:"lease"`
			Fence  int    `json:"fence"`
			Commit string `json:"commit"`
		}
		if err := decode(r, &body); err != nil {
			fail(w, r, err)
			return
		}
		item, err := api.Service.CompleteTask(r.Context(), id, body.Lease, body.Fence, body.Commit, actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, item, "")
		return
	}
	if len(parts) == 2 && parts[1] == "pull-requests" && r.Method == http.MethodPost {
		var pr domain.PullRequest
		if err := decode(r, &pr); err != nil {
			fail(w, r, err)
			return
		}
		if err := api.Service.LinkPullRequest(r.Context(), id, pr, actor(r)); err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, map[string]bool{"linked": true}, "")
		return
	}
	fail(w, r, domain.ErrNotFound)
}

func (api API) leases(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) != 2 || r.Method != http.MethodPost {
		fail(w, r, domain.ErrNotFound)
		return
	}
	id := parts[0]
	var body struct {
		Fence int    `json:"fence"`
		TTL   string `json:"ttl"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, r, err)
		return
	}
	if parts[1] == "release" {
		if err := api.Service.Release(r.Context(), id, body.Fence, actor(r)); err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, map[string]bool{"released": true}, "")
		return
	}
	if parts[1] == "heartbeat" {
		ttl := 30 * time.Minute
		if body.TTL != "" {
			var err error
			ttl, err = time.ParseDuration(body.TTL)
			if err != nil {
				fail(w, r, err)
				return
			}
		}
		lease, err := api.Service.Heartbeat(r.Context(), id, body.Fence, ttl, actor(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		write(w, r, 200, lease, "")
		return
	}
	fail(w, r, domain.ErrNotFound)
}
func (api API) trace(w http.ResponseWriter, r *http.Request, parts []string) {
	root := ""
	if len(parts) > 0 {
		root = parts[0]
	}
	items, err := api.Service.Trace(r.Context(), root, actor(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, r, 200, items, "")
}
func (api API) impact(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) != 1 {
		fail(w, r, errors.New("requirement ID is required"))
		return
	}
	items, err := api.Service.Impact(r.Context(), parts[0], actor(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, r, 200, items, "")
}
func (api API) audit(w http.ResponseWriter, r *http.Request) {
	page, err := api.Service.ListAudit(r.Context(), r.URL.Query().Get("entity"), r.URL.Query().Get("cursor"), limit(r), actor(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, r, 200, page.Items, page.NextCursor)
}
func (api API) render(w http.ResponseWriter, r *http.Request) {
	items, err := api.Service.Trace(r.Context(), r.URL.Query().Get("root"), actor(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, r, http.StatusOK, application.Render(items), "")
}

func ParsePullRequest(raw string) (domain.PullRequest, error) {
	value, err := url.Parse(raw)
	if err != nil {
		return domain.PullRequest{}, err
	}
	parts := strings.Split(strings.Trim(value.Path, "/"), "/")
	if len(parts) < 4 || parts[len(parts)-2] != "pull" {
		return domain.PullRequest{}, errors.New("pull request URL is invalid")
	}
	number, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return domain.PullRequest{}, err
	}
	return domain.PullRequest{Repository: value.Host + "/" + strings.Join(parts[:2], "/"), Number: number, URL: raw}, nil
}
