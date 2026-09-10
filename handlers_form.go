package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

type customFormField struct {
	Type       string `json:"type"`
	Label      string `json:"label"`
	HelperText string `json:"helper_text,omitempty"`
}

type formRequest struct {
	Phone                  string            `json:"Phone"`
	Title                  string            `json:"Title,omitempty"`
	Header                 string            `json:"Header,omitempty"`
	Body                   string            `json:"Body"`
	Footer                 string            `json:"Footer,omitempty"`
	FlowID                 string            `json:"FlowID,omitempty"`
	FlowToken              string            `json:"FlowToken,omitempty"`
	FlowCTA                string            `json:"FlowCTA,omitempty"`
	FullNameVisible        bool              `json:"FullNameVisible"`
	PhoneNumberVisible     bool              `json:"PhoneNumberVisible"`
	EmailVisible           bool              `json:"EmailVisible"`
	DeliveryAddressVisible bool              `json:"DeliveryAddressVisible"`
	CpfOrCnpjVisible       bool              `json:"CpfOrCnpjVisible"`
	CitizenshipCardVisible bool              `json:"CitizenshipCardVisible"`
	OfferName              string            `json:"OfferName,omitempty"`
	OfferDescription       string            `json:"OfferDescription,omitempty"`
	CustomFields           []customFormField `json:"CustomFields,omitempty"`
	Id                     string            `json:"Id,omitempty"`
}

func (s *server) SendForm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfoVal := r.Context().Value("userinfo")
		if userInfoVal == nil {
			s.Respond(w, r, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}

		txtid := userInfoVal.(Values).Get("Id")

		client := clientManager.GetWhatsmeowClient(txtid)
		if client == nil {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("no session"))
			return
		}

		decoder := json.NewDecoder(r.Body)

		var t formRequest
		if err := decoder.Decode(&t); err != nil {
			s.Respond(w, r, http.StatusBadRequest, errors.New("could not decode payload"))
			return
		}

		if t.Phone == "" {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing Phone"))
			return
		}

		if t.Body == "" {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing Body"))
			return
		}

		if t.FlowID == "" {
			t.FlowID = "3120538731449732"
		}

		if t.FlowToken == "" {
			t.FlowToken = "Q1VTVE9NRVJfSU5GTw=="
		}

		if t.FlowCTA == "" {
			t.FlowCTA = "__localize:FLOWS_COMPLETE_FORM_BUTTON_TITLE"
		}

		recipient, err := validateMessageFields(
			r.Context(),
			client,
			t.Phone,
			nil,
			nil,
		)
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, err)
			return
		}

		msgid := t.Id
		if msgid == "" {
			msgid = client.GenerateMessageID()
		}

		data := map[string]interface{}{
			"full_name_visible":        t.FullNameVisible,
			"phone_number_visible":     t.PhoneNumberVisible,
			"email_visible":            t.EmailVisible,
			"delivery_address_visible": t.DeliveryAddressVisible,
			"cpf_or_cnpj_visible":      t.CpfOrCnpjVisible,
			"citizenship_card_visible": t.CitizenshipCardVisible,
		}

		if len(t.CustomFields) > 0 {
			data["custom_fields"] = t.CustomFields
		}

		if t.OfferName != "" {
			data["offer_name"] = t.OfferName
		}

		if t.OfferDescription != "" {
			data["offer_description"] = t.OfferDescription
		}

		buttonParams := map[string]interface{}{
			"flow_message_version": "4",
			"flow_id":              t.FlowID,
			"flow_cta":             t.FlowCTA,
			"flow_action":          "navigate",
			"flow_token":           t.FlowToken,
			"form_type":            "template",
			"well_version":         "V700",
			"flow_action_payload": map[string]interface{}{
				"screen": "contact_details",
				"data":   data,
			},
		}

		buttonParamsBytes, err := json.Marshal(buttonParams)
		if err != nil {
			s.Respond(
				w,
				r,
				http.StatusInternalServerError,
				fmt.Errorf("failed to marshal form params: %w", err),
			)
			return
		}

		interactiveMsg := &waE2E.InteractiveMessage{
			Body: &waE2E.InteractiveMessage_Body{
				Text: proto.String(t.Body),
			},
			InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
				NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
					Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
						{
							Name:             proto.String("galaxy_message"),
							ButtonParamsJSON: proto.String(string(buttonParamsBytes)),
						},
					},
					MessageParamsJSON: proto.String("{}"),
					MessageVersion:    proto.Int32(3),
				},
			},
		}

		headerText := t.Title
		if headerText == "" {
			headerText = t.Header
		}

		if headerText != "" {
			interactiveMsg.Header = &waE2E.InteractiveMessage_Header{
				Title: proto.String(headerText),
			}
		}

		if t.Footer != "" {
			interactiveMsg.Footer = &waE2E.InteractiveMessage_Footer{
				Text: proto.String(t.Footer),
			}
		}

		msg := &waE2E.Message{
			InteractiveMessage: interactiveMsg,
		}

		extraNodes := []waBinary.Node{
			{
				Tag: "biz",
				Content: []waBinary.Node{
					{
						Tag: "interactive",
						Attrs: waBinary.Attrs{
							"type": "native_flow",
							"v":    "1",
						},
						Content: []waBinary.Node{
							{
								Tag: "native_flow",
								Attrs: waBinary.Attrs{
									"name": "mixed",
									"v":    "9",
								},
							},
						},
					},
				},
			},
		}

		resp, err := client.SendMessage(
			r.Context(),
			recipient,
			msg,
			whatsmeow.SendRequestExtra{
				ID:              msgid,
				AdditionalNodes: &extraNodes,
			},
		)
		if err != nil {
			s.Respond(
				w,
				r,
				http.StatusInternalServerError,
				fmt.Errorf("failed to send form message: %v", err),
			)
			return
		}

		historyStr := userInfoVal.(Values).Get("History")
		historyLimit, _ := strconv.Atoi(historyStr)

		s.saveOutgoingMessageToHistory(
			txtid,
			recipient.String(),
			msgid,
			"form",
			t.Body,
			"",
			historyLimit,
		)

		token := userInfoVal.(Values).Get("Token")

		s.publishSentMessageEvent(
			token,
			txtid,
			txtid,
			recipient,
			msgid,
			msg,
			resp.Timestamp,
		)

		responseJSON, _ := json.Marshal(map[string]interface{}{
			"Details":   "Sent",
			"Timestamp": resp.Timestamp.Unix(),
			"Id":        msgid,
			"FlowToken": t.FlowToken,
		})

		s.Respond(w, r, http.StatusOK, string(responseJSON))
	}
}