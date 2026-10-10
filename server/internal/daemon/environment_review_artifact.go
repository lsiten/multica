package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// reviewRunInlineMax is the in-band threshold for a review result. A result
// whose encoded form exceeds it is written to a private artifact and
// referenced instead of being carried through the 256 KiB runtimeproc control
// channel. The threshold leaves headroom below the transport cap for the
// status/receipt envelope.
const reviewRunInlineMax = 128 << 10

// reviewRunResponse is the child's review.run result. It carries either a
// small in-band result or a reference to a large artifact in the private
// namespace, so an oversized review result is never dropped by the transport.
type reviewRunResponse struct {
	Result   *protocol.LocalReviewResult          `json:"result,omitempty"`
	Artifact *runtimeproc.ReviewArtifactReference `json:"artifact,omitempty"`
}

// reviewRunResult runs the review in the child and returns a small in-band
// result or a reference to a large artifact, preserving the exact result shape.
// A transport or namespace failure is returned, never a silent local fallback.
func (s *environmentProcessService) reviewRunResult(ctx context.Context, request runtimeproc.Request, command protocol.LocalReviewCommand) (json.RawMessage, *runtimeproc.Error) {
	result := s.daemon.runRemoteReview(ctx, command)
	return s.encodeReviewRunResult(request, &result)
}

// encodeReviewRunResult splits a review result: small results travel in-band;
// oversized results are written to the private artifact namespace and
// referenced. The artifact name is derived only from the request id, so a
// returned reference can never redirect a read to a different file.
func (s *environmentProcessService) encodeReviewRunResult(request runtimeproc.Request, result *protocol.LocalReviewResult) (json.RawMessage, *runtimeproc.Error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return environmentOperationResult(nil, err)
	}
	if len(raw) <= reviewRunInlineMax {
		return environmentOperationResult(&reviewRunResponse{Result: result}, nil)
	}
	if err := runtimeproc.PrepareReviewArtifactNamespace(s.bootstrap.Root); err != nil {
		return environmentOperationResult(nil, err)
	}
	namespace := runtimeproc.ReviewArtifactNamespace(s.bootstrap.Root)
	name := runtimeproc.ReviewArtifactName(request.RequestID)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	reference := &runtimeproc.ReviewArtifactReference{RequestID: request.RequestID, Hash: hash, Length: int64(len(raw))}
	// A prior attempt with the same request id may already have published this
	// exact artifact; reference it idempotently rather than overwrite it.
	if _, err := runtimeproc.ReadReviewArtifact(namespace, name, hash, int64(len(raw))); err == nil {
		return environmentOperationResult(&reviewRunResponse{Artifact: reference}, nil)
	}
	if err := runtimeproc.PublishReviewArtifact(namespace, name, raw); err != nil {
		return environmentOperationResult(nil, err)
	}
	return environmentOperationResult(&reviewRunResponse{Artifact: reference}, nil)
}

// routeReviewToChild forwards a review to the owned environment service and
// returns the exact result, reading a large result from the private artifact
// namespace. A transport failure is uncertain and is not retried as a local
// Git operation.
func (c *environmentProcessClient) routeReviewToChild(ctx context.Context, command protocol.LocalReviewCommand) (protocol.LocalReviewResult, error) {
	var response reviewRunResponse
	if err := c.mutation(ctx, "review.run", command, &response); err != nil {
		return protocol.LocalReviewResult{}, err
	}
	if response.Result != nil {
		return *response.Result, nil
	}
	if response.Artifact == nil {
		return protocol.LocalReviewResult{}, errors.New("review run returned neither result nor artifact")
	}
	body, err := c.readReviewArtifact(response.Artifact)
	if err != nil {
		return protocol.LocalReviewResult{}, err
	}
	var result protocol.LocalReviewResult
	if err := json.Unmarshal(body, &result); err != nil {
		return protocol.LocalReviewResult{}, err
	}
	return result, nil
}

// readReviewArtifact re-derives the artifact name from the request id and reads
// the bounded private result, validating ownership, mode, hash and length. It
// never trusts a path from the record.
func (c *environmentProcessClient) readReviewArtifact(ref *runtimeproc.ReviewArtifactReference) ([]byte, error) {
	if ref == nil || ref.RequestID == "" {
		return nil, errors.New("review artifact reference is incomplete")
	}
	namespace := runtimeproc.ReviewArtifactNamespace(c.bootstrap.Root)
	name := runtimeproc.ReviewArtifactName(ref.RequestID)
	return runtimeproc.ReadReviewArtifact(namespace, name, ref.Hash, ref.Length)
}
