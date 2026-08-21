package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/elsell/reqdb/internal/application"
	"github.com/elsell/reqdb/internal/domain"
	"gopkg.in/yaml.v3"
)

var requirementContentOptions = []string{"--level", "--title", "--statement", "--refines", "--depends-on"}
var taskContentOptions = []string{"--title", "--description", "--priority", "--requirement", "--depends-on"}
var reviewContentOptions = []string{"--commit", "--verdict", "--task", "--finding"}

type reviewDocument struct {
	Commit   string                 `json:"commit" yaml:"commit"`
	Verdict  string                 `json:"verdict" yaml:"verdict"`
	TaskID   string                 `json:"task_id" yaml:"task_id"`
	Findings []domain.ReviewFinding `json:"findings" yaml:"findings"`
}

func reviewInput(args []string) (reviewDocument, error) {
	path := fileOption(args)
	if path != "" {
		if hasAnyOption(args, reviewContentOptions...) {
			return reviewDocument{}, fmt.Errorf("--from-file cannot be combined with review content flags")
		}
		return decodeInput(path, func(reader io.Reader) (reviewDocument, error) {
			var input reviewDocument
			err := yaml.NewDecoder(reader).Decode(&input)
			return input, err
		})
	}
	if option(args, "--commit") == "" || option(args, "--verdict") == "" {
		return reviewDocument{}, fmt.Errorf("--commit and --verdict are required")
	}
	input := reviewDocument{Commit: option(args, "--commit"), Verdict: option(args, "--verdict"), TaskID: option(args, "--task")}
	for _, message := range options(args, "--finding") {
		input.Findings = append(input.Findings, domain.ReviewFinding{Message: message})
	}
	return input, nil
}

func requirementInput(args []string, action string) (domain.RequirementInput, error) {
	path := fileOption(args)
	if path != "" {
		if hasAnyOption(args, requirementContentOptions...) {
			return domain.RequirementInput{}, fmt.Errorf("--from-file cannot be combined with requirement content flags")
		}
		return readRequirement(path)
	}
	if !hasAnyOption(args, requirementContentOptions...) {
		return domain.RequirementInput{}, fmt.Errorf("use requirement flags or --from-file")
	}
	if len(args) < 2 || strings.HasPrefix(args[1], "-") {
		return domain.RequirementInput{}, fmt.Errorf("a requirement ID is required with content flags")
	}
	revision := 1
	if action == "update" {
		expected, err := requiredInt(args, "--expected")
		if err != nil {
			return domain.RequirementInput{}, err
		}
		revision = expected + 1
	}
	input := domain.RequirementInput{
		Schema:    "requirement/v1",
		ID:        args[1],
		Level:     option(args, "--level"),
		Revision:  revision,
		Title:     option(args, "--title"),
		Statement: option(args, "--statement"),
	}
	input.Links.Refines = options(args, "--refines")
	input.Links.DependsOn = options(args, "--depends-on")
	return input, nil
}

func taskInput(args []string) (domain.TaskInput, error) {
	path := fileOption(args)
	if path != "" {
		if hasAnyOption(args, taskContentOptions...) {
			return domain.TaskInput{}, fmt.Errorf("--from-file cannot be combined with task content flags")
		}
		return readTask(path)
	}
	if !hasAnyOption(args, taskContentOptions...) {
		return domain.TaskInput{}, fmt.Errorf("use task flags or --from-file")
	}
	if len(args) < 2 || strings.HasPrefix(args[1], "-") {
		return domain.TaskInput{}, fmt.Errorf("a task ID is required with content flags")
	}
	priority, err := strconv.Atoi(option(args, "--priority"))
	if err != nil {
		return domain.TaskInput{}, fmt.Errorf("--priority must be an integer")
	}
	input := domain.TaskInput{
		Schema:      "task/v1",
		ID:          args[1],
		Title:       option(args, "--title"),
		Description: option(args, "--description"),
		Priority:    priority,
		DependsOn:   options(args, "--depends-on"),
	}
	for _, value := range options(args, "--requirement") {
		reference, purpose, ok := strings.Cut(value, ":")
		if !ok || reference == "" || purpose == "" {
			return domain.TaskInput{}, fmt.Errorf("--requirement must use REQUIREMENT@REVISION:PURPOSE")
		}
		input.Requirements = append(input.Requirements, domain.TaskRequirementInput{Requirement: reference, Purpose: purpose})
	}
	return input, nil
}

func fileOption(args []string) string {
	if value := option(args, "--from-file"); value != "" {
		return value
	}
	return option(args, "-f")
}

func readRequirement(path string) (domain.RequirementInput, error) {
	return decodeInput(path, application.DecodeRequirement)
}

func readTask(path string) (domain.TaskInput, error) {
	return decodeInput(path, application.DecodeTask)
}

func decodeInput[T any](path string, decode func(io.Reader) (T, error)) (T, error) {
	if path == "-" {
		return decode(os.Stdin)
	}
	file, err := os.Open(path)
	if err != nil {
		var zero T
		return zero, err
	}
	defer file.Close()
	return decode(file)
}

func options(args []string, name string) []string {
	var values []string
	for index, value := range args {
		if value == name && index+1 < len(args) {
			values = append(values, args[index+1])
		}
		if strings.HasPrefix(value, name+"=") {
			values = append(values, strings.TrimPrefix(value, name+"="))
		}
	}
	return values
}

func hasAnyOption(args []string, names ...string) bool {
	for _, name := range names {
		if option(args, name) != "" {
			return true
		}
	}
	return false
}
