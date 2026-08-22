package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/elsell/reqdb/internal/client"
	"github.com/elsell/reqdb/internal/domain"
	"golang.org/x/term"
)

type serverCredential struct {
	Token   string `json:"token"`
	Project string `json:"project,omitempty"`
}
type credentialFile struct {
	Servers map[string]serverCredential `json:"servers"`
}

func canonicalServer(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("server must be an absolute URL")
	}
	parsed.Fragment = ""
	parsed.RawQuery = ""
	return parsed.String(), nil
}
func credentialPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "reqdb", "token.json"), nil
}
func loadCredentials() (credentialFile, error) {
	config := credentialFile{Servers: map[string]serverCredential{}}
	path, err := credentialPath()
	if err != nil {
		return config, err
	}
	value, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(value, &config); err != nil {
		return config, fmt.Errorf("read credentials: %w", err)
	}
	if config.Servers == nil {
		config.Servers = map[string]serverCredential{}
	}
	return config, nil
}
func saveCredentials(config credentialFile) error {
	path, err := credentialPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(path), 0700)
	value, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	value = append(value, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".token-*.json")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(value); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func promptPassword() (string, error) {
	fmt.Fprint(os.Stderr, "Password: ")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		value, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(value)), err
	}
	value, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(value), err
}
func login(server string) error {
	canonical, err := canonicalServer(server)
	if err != nil {
		return err
	}
	token, err := promptPassword()
	if err != nil {
		return err
	}
	if token == "" {
		return errors.New("password cannot be empty")
	}
	api := client.Client{BaseURL: canonical, Token: token, HTTP: http.DefaultClient}
	if _, err := api.Do(context.Background(), http.MethodGet, "/v1/auth/check", nil); err != nil {
		return err
	}
	config, err := loadCredentials()
	if err != nil {
		return err
	}
	entry := config.Servers[canonical]
	entry.Token = token
	config.Servers[canonical] = entry
	if err := saveCredentials(config); err != nil {
		return err
	}
	fmt.Println("Logged in to", canonical)
	return nil
}

func singleProject(ctx context.Context, api client.Client) (string, error) {
	envelope, err := api.Do(ctx, http.MethodGet, "/v1/projects", nil)
	if err != nil {
		return "", err
	}
	var projects []domain.Project
	if err := json.Unmarshal(envelope.Data, &projects); err != nil {
		return "", err
	}
	if len(projects) == 1 {
		return projects[0].ID, nil
	}
	if len(projects) == 0 {
		return "", errors.New("no projects exist; create one with reqdb project create")
	}
	return "", errors.New("multiple projects exist; select one with --project, REQDB_PROJECT, or reqdb project use")
}

func projectCommand(ctx context.Context, api client.Client, args []string, jsonOutput bool, config credentialFile) error {
	if len(args) == 0 {
		return errors.New("a project action is required (list, get, create, or use)")
	}
	switch args[0] {
	case "list":
		return call(ctx, api, http.MethodGet, "/v1/projects", nil, jsonOutput)
	case "get":
		if len(args) < 2 {
			return errors.New("a project ID is required")
		}
		return call(ctx, api, http.MethodGet, "/v1/projects/"+url.PathEscape(args[1]), nil, jsonOutput)
	case "create":
		if len(args) < 2 {
			return errors.New("a project ID is required")
		}
		name := option(args, "--name")
		if name == "" {
			name = args[1]
		}
		return call(ctx, api, http.MethodPost, "/v1/projects", domain.ProjectInput{ID: args[1], Name: name, Description: option(args, "--description")}, jsonOutput)
	case "use":
		if len(args) < 2 {
			return errors.New("a project ID is required")
		}
		if _, err := api.Do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(args[1]), nil); err != nil {
			return err
		}
		server, err := canonicalServer(api.BaseURL)
		if err != nil {
			return err
		}
		entry := config.Servers[server]
		entry.Project = args[1]
		config.Servers[server] = entry
		if err := saveCredentials(config); err != nil {
			return err
		}
		fmt.Printf("Using project %s on %s\n", args[1], server)
		return nil
	default:
		return fmt.Errorf("unknown project action %q", args[0])
	}
}
