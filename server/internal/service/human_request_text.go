package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var ordinalHumanChoice = regexp.MustCompile(`^(?:选|选择)?第?([1-4])(?:项|个选项)?$`)

// HumanReplyOrigin records the exact member text and the reply produced by the decision transaction.
type HumanReplyOrigin struct {
	Channel   string `json:"channel"`
	Text      string `json:"text"`
	ReplyID   string `json:"reply_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// HumanTextReply binds ordinary text to the request revision the member actually saw.
type HumanTextReply struct {
	Revision int64  `json:"revision"`
	Text     string `json:"text"`
	Channel  string `json:"channel"`
	ScopeID  string `json:"scope_id"`
}

// MatchHumanTextAnswer accepts an entire, unambiguous answer, never a fragment of a discussion.
func MatchHumanTextAnswer(request HumanRequestInput, revision int64, text string) (HumanRequestAnswer, error) {
	answer := HumanRequestAnswer{Revision: revision}
	if request.ResponseMode != "chat_or_card" {
		return answer, fmt.Errorf("%w: request requires its card control", ErrHumanRequestInput)
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return answer, ErrHumanRequestInput
	}
	switch request.Kind {
	case "input":
		answer.Decision, answer.Answer = "input", text
	case "choice":
		matches := map[string]bool{}
		for index, choice := range request.Choices {
			if trimmed == strings.TrimSpace(choice.Label) {
				matches[choice.ID] = true
			}
			if strings.EqualFold(trimmed, string(rune('A'+index))) || trimmed == fmt.Sprint(index+1) {
				matches[choice.ID] = true
			}
		}
		if ordinal := ordinalHumanChoice.FindStringSubmatch(trimmed); len(ordinal) == 2 {
			index := int(ordinal[1][0] - '1')
			if index < len(request.Choices) {
				matches[request.Choices[index].ID] = true
			}
		}
		if len(matches) != 1 {
			return answer, fmt.Errorf("%w: text does not uniquely match a choice", ErrHumanRequestInput)
		}
		for id := range matches {
			answer.Decision, answer.Answer = "choice", id
		}
	default:
		return answer, ErrHumanRequestInput
	}
	if err := answer.Validate(request); err != nil {
		return answer, err
	}
	return answer, nil
}

func humanAnswerLabel(request HumanRequestInput, answer HumanRequestAnswer) string {
	for _, choice := range request.Choices {
		if choice.ID == answer.Answer {
			return choice.Label
		}
	}
	return answer.Answer
}

// HumanResponseReceipt records a persisted answer independently of a request card.
type HumanResponseReceipt struct {
	RequestID string `json:"request_id"`
	Revision  int64  `json:"revision"`
	Label     string `json:"label"`
}

func ResponseReceipt(request db.HumanRequest) *HumanResponseReceipt {
	var response struct {
		HumanRequestAnswer
		Origin *HumanReplyOrigin `json:"origin"`
	}
	var input HumanRequestInput
	if request.Status != "answered" || json.Unmarshal(request.Response, &response) != nil || response.Origin == nil || response.Origin.ReplyID == "" || json.Unmarshal(request.Payload, &input) != nil {
		return nil
	}
	return &HumanResponseReceipt{RequestID: util.UUIDToString(request.ID), Revision: response.Revision, Label: humanAnswerLabel(input, response.HumanRequestAnswer)}
}
