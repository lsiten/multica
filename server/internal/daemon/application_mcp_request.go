package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"

	"github.com/multica-ai/multica/server/internal/util"
)

type applicationMCPArguments struct {
	Action        string          `json:"action"`
	ApplicationID string          `json:"application_id"`
	ProjectID     string          `json:"project_id"`
	OperationID   string          `json:"operation_id"`
	InstanceID    string          `json:"instance_id"`
	EndpointID    string          `json:"endpoint_id"`
	Revision      int64           `json:"revision"`
	Cursor        string          `json:"cursor"`
	Limit         int             `json:"limit"`
	Body          json.RawMessage `json:"body"`
	Cancel        bool            `json:"cancel"`
}

func applicationMCPUUID(raw, field string) (string, error) {
	id, err := util.ParseUUID(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be a UUID", field)
	}
	return util.UUIDToString(id), nil
}

func (s *applicationMCPServer) invoke(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var args applicationMCPArguments
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("application arguments must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return nil, errors.New("invalid application tool arguments")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("application arguments must contain one JSON object")
	}
	method, path := "GET", "/api/applications"
	if name != "multica_application_status" && !(name == "multica_application_catalog" && (args.Action == "list" || args.Action == "create")) {
		id, err := applicationMCPUUID(args.ApplicationID, "application_id")
		if err != nil {
			return nil, err
		}
		path += "/" + id
	}
	switch name {
	case "multica_application_catalog":
		switch args.Action {
		case "list":
			if args.ProjectID != "" {
				id, err := applicationMCPUUID(args.ProjectID, "project_id")
				if err != nil {
					return nil, err
				}
				path += "?project_id=" + url.QueryEscape(id)
			}
		case "get":
		case "create":
			method = "POST"
		case "update":
			method = "PATCH"
		case "delete":
			if args.Revision < 1 {
				return nil, errors.New("delete requires the current revision")
			}
			method = "DELETE"
			path += "?revision=" + strconv.FormatInt(args.Revision, 10)
		case "plan":
			path += "/plan"
		default:
			return nil, errors.New("unknown application catalog action")
		}
	case "multica_application_control":
		method = "POST"
		path += "/operations"
	case "multica_application_status":
		path += "/board"
	case "multica_application_operations":
		path += "/operations"
		if args.OperationID != "" {
			id, err := applicationMCPUUID(args.OperationID, "operation_id")
			if err != nil {
				return nil, err
			}
			path += "/" + id
		}
		if args.Cancel {
			if args.OperationID == "" {
				return nil, errors.New("operation_id is required to cancel")
			}
			method = "POST"
			path += "/cancel"
		}
	case "multica_application_logs":
		id, err := applicationMCPUUID(args.InstanceID, "instance_id")
		if err != nil {
			return nil, err
		}
		if args.Limit == 0 {
			args.Limit = 65536
		}
		if args.Limit < 1 || args.Limit > 65536 || len(args.Cursor) > 512 {
			return nil, errors.New("invalid application log limit or cursor")
		}
		path += "/instances/" + id + "/logs?" + url.Values{"cursor": {args.Cursor}, "limit": {strconv.Itoa(args.Limit)}}.Encode()
	case "multica_application_service_access":
		id, err := applicationMCPUUID(args.EndpointID, "endpoint_id")
		if err != nil {
			return nil, err
		}
		method = "POST"
		path += "/endpoints/" + id + "/service-access"
	default:
		return nil, errors.New("unknown application tool")
	}
	var body map[string]any
	if method == "PATCH" || method == "POST" && name != "multica_application_service_access" && name != "multica_application_operations" {
		if len(args.Body) > 64<<10 || json.Unmarshal(args.Body, &body) != nil || body == nil {
			return nil, errors.New("body must be a JSON object of at most 64 KiB")
		}
	}
	var result any
	var err error
	switch method {
	case "GET":
		err = s.client.GetJSON(ctx, path, &result)
	case "POST":
		err = s.client.PostJSON(ctx, path, body, &result)
	case "PATCH":
		err = s.client.PatchJSON(ctx, path, body, &result)
	case "DELETE":
		err = s.client.DeleteJSON(ctx, path)
		result = map[string]bool{"deleted": true}
	}
	if err != nil {
		return nil, fmt.Errorf("application request failed: %w", err)
	}
	return result, nil
}
