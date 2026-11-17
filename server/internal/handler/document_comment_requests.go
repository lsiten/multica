package handler

type CreateDocumentCommentRequest struct {
	SubmissionID *string                 `json:"submission_id"`
	Resource     DocumentCommentResource `json:"resource"`
	Content      string                  `json:"content"`
	ParentID     *string                 `json:"parent_id"`
	Anchor       *DocumentCommentAnchor  `json:"anchor"`
}

type QueryDocumentCommentsRequest struct {
	Resource DocumentCommentResource `json:"resource"`
	Offset   int32                   `json:"offset"`
}

type ResolveDocumentCommentRequest struct {
	Resource         DocumentCommentResource `json:"resource"`
	Resolved         *bool                   `json:"resolved"`
	ExpectedRevision int64                   `json:"expected_revision"`
}
