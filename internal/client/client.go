package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/elsell/reqdb/internal/application"
)

type Client struct {
	BaseURL, ActorID string
	HTTP             *http.Client
}
type Envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code          string `json:"code"`
		Message       string `json:"message"`
		CorrelationID string `json:"correlation_id"`
	} `json:"error"`
	Meta struct {
		CorrelationID string `json:"correlation_id"`
		NextCursor    string `json:"next_cursor"`
	} `json:"meta"`
}

type APIError struct {
	Code          string
	Message       string
	CorrelationID string
}

func (err APIError) Error() string { return err.Message }

func (client Client) Do(ctx context.Context, method, path string, body any) (Envelope, error) {
	var reader io.Reader
	if body != nil {
		value, err := json.Marshal(body)
		if err != nil {
			return Envelope{}, err
		}
		reader = bytes.NewReader(value)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(client.BaseURL, "/")+path, reader)
	if err != nil {
		return Envelope{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Actor-ID", client.ActorID)
	req.Header.Set("X-Correlation-ID", application.NewID())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.HTTP.Do(req)
	if err != nil {
		return Envelope{}, err
	}
	defer response.Body.Close()
	var envelope Envelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return envelope, err
	}
	if envelope.Error != nil {
		return envelope, APIError{Code: envelope.Error.Code, Message: envelope.Error.Message, CorrelationID: envelope.Error.CorrelationID}
	}
	return envelope, nil
}

func (client Client) Render(ctx context.Context, path string) (string, error) {
	envelope, err := client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(envelope.Data, &value); err != nil {
		return "", err
	}
	return value, nil
}
