package handler

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"unicode"
)

const (
	identityEmailAction          = "email"
	identityEmailActivity        = "agent_identity_email_sent"
	identityEmailMaxRequestBytes = 80 << 10
	identityEmailMaxSubjectBytes = 256
	identityEmailMaxBodyBytes    = 64 << 10
)

type identityEmailRequest struct {
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
}

// identityEmailActionAllowed reads the task agent's explicit capability grant.
// It intentionally fails closed for missing, malformed, or differently shaped
// runtime_config values. The daemon may expose the local tool only after this
// same grant has been evaluated; the server repeats the check at execution
// time so a stale daemon cannot retain an old capability.
func identityEmailActionAllowed(raw []byte) bool {
	var envelope map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &envelope) != nil {
		return false
	}
	policyRaw, ok := envelope["multica_autonomy"]
	if !ok {
		return false
	}
	var policy map[string]json.RawMessage
	if json.Unmarshal(policyRaw, &policy) != nil {
		return false
	}
	var actions []string
	if rawActions, ok := policy["allowed_identity_actions"]; !ok || json.Unmarshal(rawActions, &actions) != nil {
		return false
	}
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action), identityEmailAction) {
			return true
		}
	}
	return false
}

func normalizeIdentityEmailRecipient(raw string) (string, string, bool) {
	recipient := strings.TrimSpace(raw)
	if recipient == "" || len([]byte(recipient)) > 320 || strings.ContainsAny(recipient, "\r\n") {
		return "", "", false
	}
	parsed, err := mail.ParseAddress(recipient)
	if err != nil || parsed.Address != recipient {
		return "", "", false
	}
	at := strings.LastIndexByte(recipient, '@')
	if at <= 0 || at == len(recipient)-1 {
		return "", "", false
	}
	return recipient, strings.ToLower(recipient[at+1:]), true
}

func validIdentityEmailText(subject, body string) bool {
	if len([]byte(subject)) == 0 || len([]byte(subject)) > identityEmailMaxSubjectBytes {
		return false
	}
	for _, r := range subject {
		if unicode.IsControl(r) {
			return false
		}
	}
	if len([]byte(body)) == 0 || len([]byte(body)) > identityEmailMaxBodyBytes {
		return false
	}
	return !strings.ContainsRune(body, '\x00') && !strings.ContainsRune(body, '\r')
}

// Retired server execution endpoint: old daemons must upgrade instead of using
// the workspace login-email account to send Agent messages.
func (h *Handler) SendAgentEmail(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusGone, "agent email executes on the runtime daemon; upgrade the daemon and configure host SMTP")
}
