// handlers_pix.go
//
// URUPIX custom endpoints.

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
)

type pixRequest struct {
	Phone        string `json:"Phone"`
	MerchantName string `json:"MerchantName"`
	PixKey       string `json:"PixKey"`
	PixKeyType   string `json:"PixKeyType"` // PHONE, CPF, CNPJ, EMAIL, EVP
	Id           string `json:"Id,omitempty"`
}

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

func generateAlphanumericID(length int) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	bytes := make([]byte, length)
	_, _ = rand.Read(bytes)

	for i := range bytes {
		bytes[i] = charset[int(bytes[i])%len(charset)]
	}

	return string(bytes)
}

func (s *server) SendPix() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		txtid := r.Context().Value("userinfo").(Values).Get("Id")

		client := clientManager.GetWhatsmeowClient(txtid)
		if client == nil {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("no session"))
			return
		}

		var t pixRequest
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
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

		recipient, err := validateMessageFields(r.Context(), client, t.Phone, nil, nil)
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, err)
			return
		}

		msgid := t.Id
		if msgid == "" {
			msgid = client.GenerateMessageID()
		}

		params := paymentInfoParams{
			ReferenceID:          generateAlphanumericID(11),
			Type:                 "physical-goods",
			PaymentConfiguration: "merchant_categorization_code",
			PaymentSettings: []paymentSetting{{
				Type: "pix_static_code",
				PixStaticCode: pixStaticCode{
					MerchantName: t.MerchantName,
					Key:          t.PixKey,
					KeyType:      pixKeyType,
				},
			}},
			Currency: "BRL",
			TotalAmount: totalAmount{
				Value:  0,
				Offset: 1000,
			},
			OrderRequestID: generateAlphanumericID(11),
			Order: orderDetails{
				Status: "payment_requested",
				Items: []orderItem{{
					Quantity:   0,
					RetailerID: generateAlphanumericID(11),
					Amount: orderItemAmount{
						Offset: 1000,
						Value:  0,
					},
					Name:          "",
					ProductID:     "",
					IsCustomItem:  false,
					IsQuantitySet: false,
				}},
				Subtotal: totalAmount{
					Value:  0,
					Offset: 1000,
				},
				Tax:      nil,
				Shipping: nil,
				Discount: nil,
			},
			Referral: "chat_attachment",
		}

		msg, additionalNodes, err := buildNativeFlowMessage(nativeFlowMessage{
			Buttons: []nativeFlowButton{{
				Name:   "payment_info",
				Params: params,
			}},
			Version: 1,
		})
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, fmt.Errorf("failed to build payment message: %w", err))
			return
		}

		resp, err := client.SendMessage(r.Context(), recipient, msg, whatsmeow.SendRequestExtra{
			ID:              msgid,
			AdditionalNodes: &additionalNodes,
		})
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, fmt.Errorf("failed to send payment message: %w", err))
			return
		}

		historyStr := r.Context().Value("userinfo").(Values).Get("History")
		historyLimit, _ := strconv.Atoi(historyStr)
		s.saveOutgoingMessageToHistory(
			txtid,
			recipient.String(),
			msgid,
			"payment",
			fmt.Sprintf("%s: %s", pixKeyType, t.PixKey),
			"",
			historyLimit,
		)

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