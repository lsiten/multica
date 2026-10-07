package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func resolveApplicationConnection(ctx context.Context, q *db.Queries, command *protocol.ApplicationControlCommand, source, target db.ApplicationInstance, binding protocol.ApplicationConnection) error {
	revision, err := q.GetApplicationRevision(ctx, db.GetApplicationRevisionParams{WorkspaceID: target.WorkspaceID, ApplicationID: target.ApplicationID, Revision: target.Revision})
	if err != nil {
		return err
	}
	var config protocol.ApplicationConfig
	if err := json.Unmarshal(revision.Config, &config); err != nil {
		return err
	}
	if config.Port < 1 {
		return fmt.Errorf("%w: connected dependency requires an HTTP service port", ErrInvalid)
	}
	connection := protocol.ApplicationResolvedConnection{URLVariable: binding.URLVariable, TargetInstanceID: util.UUIDToString(target.ID), TargetGeneration: target.Generation}
	if source.DaemonID == target.DaemonID {
		address := url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(config.Port)), Path: config.EntryPath}
		connection.LocalURL = address.String()
	}
	command.Connections = append(command.Connections, connection)
	return nil
}
