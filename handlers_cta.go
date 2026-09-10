// handlers_cta.go
//
// URUPIX custom endpoints: WhatsApp Interactive Call-To-Action, Call & Quick Reply buttons.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"go.mau.fi/whatsmeow"
)

type ctaButton struct {
	Type        string `json:"Type"` // "url", "call", "copy", "quick_reply"
	DisplayText string `json:"DisplayText"`
	Url         string `json:"Url,omitempty"`
	PhoneNumber string `json:"PhoneNumber,omitempty"`
	CopyCode    string `json:"CopyCode,omitempty"`
	Id          string `json:"Id,omitempty"`
}

type ctaRequest struct {
	Phone   string      `json:"Phone"`
	Title   string      `json:"Title,omitempty"`
	Header  string      `json:"Header,omitempty"`
	Body    string      `json:"Body"`
	Footer  string      `json:"Footer,omitempty"`
	Buttons []ctaButton `json:"Buttons"`
	Id      string      `json:"Id,omitempty"`
}

// phoneCleanRegex strips any non-digit characters from phone numbers (keeps leading +)
var phoneCleanRegex = regexp.MustCompile(`[^\d+]`)

func (s *server) SendCTA() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		txtid := r.Context().Value("userinfo").(Values).Get("Id")

		client := clientManager.GetWhatsmeowClient(txtid)
		if client == nil {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("no active WhatsApp session"))
			return
		}

		var t ctaRequest
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			s.Respond(w, r, http.StatusBadRequest, errors.New("could not decode JSON payload"))
			return
		}

		if t.Phone == "" {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing field: Phone"))
			return
		}
		if t.Body == "" {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing field: Body"))
			return
		}
		if len(t.Buttons) == 0 {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing field: Buttons (at least 1 button required)"))
			return
		}
		if len(t.Buttons) > 3 {
			s.Respond(w, r, http.StatusBadRequest, errors.New("WhatsApp layout limit: maximum of 3 buttons allowed per message"))
			return
		}

		hasQuickReply := false
		hasCTA := false

		for _, btn := range t.Buttons {
			switch strings.ToLower(strings.TrimSpace(btn.Type)) {
			case "quick_reply", "reply":
				hasQuickReply = true
			case "url", "cta_url", "call", "cta_call", "copy", "cta_copy":
				hasCTA = true
			}
		}

		if hasQuickReply && hasCTA {
			s.Respond(w, r, http.StatusBadRequest, errors.New("WhatsApp layout restriction: cannot mix 'quick_reply' with CTA buttons ('url', 'call', 'copy') in the same message"))
			return
		}

		recipient, err := validateMessageFields(r.Context(), client, t.Phone, nil, nil)
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, err)
			return
		}

		msgid := t.Id
		if msgid == "" {
			msgid = client.GenerateMessageID()
		}

		buttons := make([]nativeFlowButton, 0, len(t.Buttons))

		for idx, btn := range t.Buttons {
			if strings.TrimSpace(btn.DisplayText) == "" {
				s.Respond(w, r, http.StatusBadRequest, fmt.Errorf("button at index %d is missing DisplayText", idx))
				return
			}

			var button nativeFlowButton

			switch strings.ToLower(strings.TrimSpace(btn.Type)) {
			case "url", "cta_url":
				if strings.TrimSpace(btn.Url) == "" {
					s.Respond(w, r, http.StatusBadRequest, fmt.Errorf("button at index %d is type url but missing Url", idx))
					return
				}

				button = nativeFlowButton{
					Name: "cta_url",
					Params: map[string]interface{}{
						"display_text": btn.DisplayText,
						"url":          btn.Url,
						"merchant_url": btn.Url,
					},
				}

			case "call", "cta_call":
				if strings.TrimSpace(btn.PhoneNumber) == "" {
					s.Respond(w, r, http.StatusBadRequest, fmt.Errorf("button at index %d is type call but missing PhoneNumber", idx))
					return
				}

				button = nativeFlowButton{
					Name: "cta_call",
					Params: map[string]interface{}{
						"display_text": btn.DisplayText,
						"phone_number": phoneCleanRegex.ReplaceAllString(btn.PhoneNumber, ""),
					},
				}

			case "copy", "cta_copy":
				if strings.TrimSpace(btn.CopyCode) == "" {
					s.Respond(w, r, http.StatusBadRequest, fmt.Errorf("button at index %d is type copy but missing CopyCode", idx))
					return
				}

				button = nativeFlowButton{
					Name: "cta_copy",
					Params: map[string]interface{}{
						"display_text": btn.DisplayText,
						"copy_code":    btn.CopyCode,
					},
				}

			case "quick_reply", "reply":
				buttonID := strings.TrimSpace(btn.Id)
				if buttonID == "" {
					buttonID = fmt.Sprintf("btn_%d", idx)
				}

				button = nativeFlowButton{
					Name: "quick_reply",
					Params: map[string]interface{}{
						"display_text": btn.DisplayText,
						"id":           buttonID,
					},
				}

			default:
				s.Respond(w, r, http.StatusBadRequest, fmt.Errorf("unsupported button type: %s", btn.Type))
				return
			}

			buttons = append(buttons, button)
		}

		headerText := t.Title
		if headerText == "" {
			headerText = t.Header
		}

		msg, additionalNodes, err := buildNativeFlowMessage(nativeFlowMessage{
			Title:   headerText,
			Body:    t.Body,
			Footer:  t.Footer,
			Buttons: buttons,
			Version: 1,
		})
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, fmt.Errorf("failed to build CTA message: %w", err))
			return
		}

		resp, err := client.SendMessage(r.Context(), recipient, msg, whatsmeow.SendRequestExtra{
			ID:              msgid,
			AdditionalNodes: &additionalNodes,
		})
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, fmt.Errorf("failed to send CTA message: %w", err))
			return
		}

		historyStr := r.Context().Value("userinfo").(Values).Get("History")
		historyLimit, _ := strconv.Atoi(historyStr)
		s.saveOutgoingMessageToHistory(txtid, recipient.String(), msgid, "cta", t.Body, "", historyLimit)

		token := r.Context().Value("userinfo").(Values).Get("Token")
		s.publishSentMessageEvent(token, txtid, txtid, recipient, msgid, msg, resp.Timestamp)

		responseJSON, _ := json.Marshal(map[string]interface{}{
			"Details":   "Sent",
			"Timestamp": resp.Timestamp.Unix(),
			"Id":        msgid,
		})
		s.Respond(w, r, http.StatusOK, string(responseJSON))
	}
}