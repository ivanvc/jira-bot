package github

import "encoding/json"

// IssueComment holds the comment received.
type IssueComment struct {
	Action  string `json:"action"`
	Issue   `json:"issue"`
	Comment `json:"comment"`
}

// Issue holds the issue from the issue comment.
type Issue struct {
	CommentsURL string `json:"comments_url"`
	Body        string `json:"body"`
	State       string `json:"state"`
}

// Comment holds the comment from the IssueComment.
type Comment struct {
	Body      string `json:"body"`
	NodeID    string `json:"node_id"`
	ID        uint64 `json:"id"`
	Reactions `json:"reactions"`
}

// Reactions from the Comment.
type Reactions struct {
	URL string `json:"url"`
}

// Converts the IssueComment into a JSON.
func (ic IssueComment) ToJSON() ([]byte, error) {
	return json.Marshal(ic)
}
