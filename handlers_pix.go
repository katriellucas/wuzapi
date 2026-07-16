// handlers_pix.go
package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// Aligned with WuzAPI: Private lowerCamelCase matching pollRequest/listRequest
type pixRequest struct {
	Phone        string `json:"Phone"`
	MerchantName string `json:"MerchantName"`
	PixKey       string `json:"PixKey"`
	PixKeyType   string `json:"PixKeyType"` // PHONE, CPF, CNPJ, EMAIL, EVP
	Amount       int64  `json:"Amount"`     // Amount in integer format (e.g., 15000 = 15.00 BRL)
	Currency     string `json:"Currency,omitempty"`
	Id           string `json:"Id,omitempty"` // Matches SendButtons/SendMessage
}

// Private lowerCamelCase helper structures private to this package
type pixStaticCode struct {
	MerchantName string `json:"merchant_name"`
	Key          string `json:"key"`
	KeyType      string `json:"key_type"`
}

type paymentSetting struct {
	Type          string        `json:"type"`
	PixStaticCode pixStaticCode `json:"pix_static_code"`
}

type totalAmount struct {
	Value  int64 `json:"value"`
	Offset int64 `json:"offset"`
}

type orderItemAmount struct {
	Offset int64 `json:"offset"`
	Value  int64 `json:"value"`
}

type orderItem struct {
	Quantity      int             `json:"quantity"`
	RetailerID    string          `json:"retailer_id"`
	Amount        orderItemAmount `json:"amount"`
	Name          string          `json:"name"`
	ProductID     string          `json:"product_id"`
	IsCustomItem  bool            `json:"isCustomItem"`
	IsQuantitySet bool            `json:"isQuantitySet"`
}

type orderDetails struct {
	Status   string      `json:"status"`
	Items    []orderItem `json:"items"`
	Subtotal totalAmount `json:"subtotal"`
	Tax      *string     `json:"tax"`
	Shipping *string     `json:"shipping"`
	Discount *string     `json:"discount"`
}

// Kept as paymentInfoParams because it maps directly to WhatsApp's "payment_info" protocol keys
type paymentInfoParams struct {
	ReferenceID          string           `json:"reference_id"`
	Type                 string           `json:"type"`
	PaymentConfiguration string           `json:"payment_configuration"`
	PaymentSettings      []paymentSetting `json:"payment_settings"`
	Currency             string           `json:"currency"`
	TotalAmount          totalAmount      `json:"total_amount"`
	OrderRequestID       string           `json:"order_request_id"`
	Order                orderDetails     `json:"order"`
	Referral             string           `json:"referral"`
}

// Alphanumeric ID generator using crypto/rand for cryptographic safety and performance
func generateAlphanumericID(length int) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	bytes := make([]byte, length)
	_, _ = rand.Read(bytes)
	for i := range bytes {
		bytes[i] = charset[int(bytes[i])%len(charset)]
	}
	return string(bytes)
}

// Aligned with WuzAPI: Named SendPix instead of SendPaymentMessage to match SendImage/SendAudio
func (s *server) SendPix() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		txtid := r.Context().Value("userinfo").(Values).Get("Id")

		client := clientManager.GetWhatsmeowClient(txtid)
		if client == nil {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("no session"))
			return
		}

		decoder := json.NewDecoder(r.Body)
		var t pixRequest
		err := decoder.Decode(&t)
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, errors.New("could not decode payload"))
			return
		}

		if t.Phone == "" {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing Phone"))
			return
		}
		if t.MerchantName == "" {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing MerchantName"))
			return
		}
		if t.PixKey == "" {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing PixKey"))
			return
		}

		pixKeyType := strings.ToUpper(t.PixKeyType)
		if pixKeyType == "" {
			pixKeyType = "PHONE"
		}
		if t.Currency == "" {
			t.Currency = "BRL"
		}

		// Aligned with handlers.go: Uses the standard validateMessageFields helper for parsing the JID
		recipient, err := validateMessageFields(t.Phone, nil, nil)
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, err)
			return
		}

		// Aligned with handlers.go: Supports tracking messages via custom ID parameters
		msgid := t.Id
		if msgid == "" {
			msgid = client.GenerateMessageID()
		}

		refID := generateAlphanumericID(11)
		orderReqID := generateAlphanumericID(11)
		retailerID := generateAlphanumericID(11)

		params := paymentInfoParams{
			ReferenceID:          refID,
			Type:                 "physical-goods",
			PaymentConfiguration: "merchant_categorization_code",
			PaymentSettings: []paymentSetting{
				{
					Type: "pix_static_code",
					PixStaticCode: pixStaticCode{
						MerchantName: t.MerchantName,
						Key:          t.PixKey,
						KeyType:      pixKeyType,
					},
				},
			},
			Currency: t.Currency,
			TotalAmount: totalAmount{
				Value:  t.Amount,
				Offset: 1000,
			},
			OrderRequestID: orderReqID,
			Order: orderDetails{
				Status: "payment_requested",
				Items: []orderItem{
					{
						Quantity:   1,
						RetailerID: retailerID,
						Amount: orderItemAmount{
							Offset: 1000,
							Value:  t.Amount,
						},
						Name:          "Payment requested by " + t.MerchantName,
						ProductID:     "payment",
						IsCustomItem:  false,
						IsQuantitySet: false,
					},
				},
				Subtotal: totalAmount{
					Value:  t.Amount,
					Offset: 1000,
				},
				Tax:      nil,
				Shipping: nil,
				Discount: nil,
			},
			Referral: "chat_attachment",
		}

		buttonParamsBytes, err := json.Marshal(params)
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, fmt.Errorf("failed to marshal payment params: %w", err))
			return
		}

		// Strictly Native: InteractiveMessage created with empty Header and standard Body matching native WhatsApp Web packets
		msg := &waE2E.Message{
			InteractiveMessage: &waE2E.InteractiveMessage{
				Header: &waE2E.InteractiveMessage_Header{},
				Body:   &waE2E.InteractiveMessage_Body{Text: proto.String(fmt.Sprintf("%s: %s", pixKeyType, t.PixKey))},
				InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
					NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
						Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
							{
								Name:             proto.String("payment_info"),
								ButtonParamsJSON: proto.String(string(buttonParamsBytes)),
							},
						},
						MessageVersion: proto.Int32(1),
					},
				},
			},
		}

		// Essential binary nodes to enable interactive native flows on phone devices
		extraNodes := []waBinary.Node{{
			Tag: "biz",
			Content: []waBinary.Node{{
				Tag:   "interactive",
				Attrs: waBinary.Attrs{"type": "native_flow", "v": "1"},
				Content: []waBinary.Node{{
					Tag:   "native_flow",
					Attrs: waBinary.Attrs{"name": "payment_info"},
				}},
			}},
		}}

		resp, err := client.SendMessage(r.Context(), recipient, msg, whatsmeow.SendRequestExtra{
			ID:              msgid,
			AdditionalNodes: &extraNodes,
		})
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, fmt.Errorf("failed to send payment message: %v", err))
			return
		}

		// Aligned with handlers.go: Uses saveOutgoingMessageToHistory wrapper to respect DB trimming rules
		historyStr := r.Context().Value("userinfo").(Values).Get("History")
		historyLimit, _ := strconv.Atoi(historyStr)
		s.saveOutgoingMessageToHistory(txtid, recipient.String(), msgid, "payment", fmt.Sprintf("%s: %s", pixKeyType, t.PixKey), "", historyLimit)

		// Calls global event dispatcher already defined in handlers.go
		token := r.Context().Value("userinfo").(Values).Get("Token")
		s.publishSentMessageEvent(token, txtid, txtid, recipient, msgid, msg, resp.Timestamp)

		// Aligned with handlers.go: Response timestamp unified to standard Unix epoch format
		responseJSON, _ := json.Marshal(map[string]interface{}{
			"Details":   "Sent",
			"Timestamp": resp.Timestamp.Unix(),
			"Id":        msgid,
		})
		s.Respond(w, r, http.StatusOK, string(responseJSON))
	}
}
