// native_flow.go
//
// Shared builder for WhatsApp InteractiveMessage native flows.

package main

import (
	"encoding/json"
	"errors"
	"fmt"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

type nativeFlowButton struct {
	Name   string
	Params any
}

type nativeFlowMessage struct {
	Title   string
	Body    string
	Footer  string
	Buttons []nativeFlowButton
	Version int32
}

type nativeFlowProtocol int

const (
	nativeFlowProtocolUnknown nativeFlowProtocol = iota
	nativeFlowProtocolMixed
	nativeFlowProtocolPaymentInfo
)

// buildNativeFlowMessage builds both parts required for sending a native flow:
//
//   - the encrypted InteractiveMessage protobuf
//   - the plaintext <biz> transport metadata
func buildNativeFlowMessage(flow nativeFlowMessage) (*waE2E.Message, []waBinary.Node, error) {
	if len(flow.Buttons) == 0 {
		return nil, nil, errors.New("native flow requires at least one button")
	}

	if flow.Version == 0 {
		flow.Version = 1
	}

	protocol, err := detectNativeFlowProtocol(flow.Buttons)
	if err != nil {
		return nil, nil, err
	}

	protoButtons, err := buildNativeFlowButtons(flow.Buttons)
	if err != nil {
		return nil, nil, err
	}

	interactive := &waE2E.InteractiveMessage{
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
				Buttons:        protoButtons,
				MessageVersion: proto.Int32(flow.Version),
			},
		},
	}

	if flow.Title != "" {
		interactive.Header = &waE2E.InteractiveMessage_Header{
			Title:              proto.String(flow.Title),
			HasMediaAttachment: proto.Bool(false),
		}
	}

	if flow.Body != "" {
		interactive.Body = &waE2E.InteractiveMessage_Body{
			Text: proto.String(flow.Body),
		}
	}

	if flow.Footer != "" {
		interactive.Footer = &waE2E.InteractiveMessage_Footer{
			Text: proto.String(flow.Footer),
		}
	}

	msg := &waE2E.Message{
		InteractiveMessage: interactive,
	}

	nodes := buildNativeFlowProtocolNodes(protocol)

	return msg, nodes, nil
}

func buildNativeFlowButtons(buttons []nativeFlowButton) ([]*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton, error) {
	result := make(
		[]*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton,
		0,
		len(buttons),
	)

	for i, button := range buttons {
		if button.Name == "" {
			return nil, fmt.Errorf("native flow button at index %d has no name", i)
		}

		params, err := json.Marshal(button.Params)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to marshal native flow button %q params: %w",
				button.Name,
				err,
			)
		}

		result = append(
			result,
			&waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
				Name:             proto.String(button.Name),
				ButtonParamsJSON: proto.String(string(params)),
			},
		)
	}

	return result, nil
}

// detectNativeFlowProtocol determines which plaintext transport metadata is
// required by the native-flow buttons.
func detectNativeFlowProtocol(buttons []nativeFlowButton) (nativeFlowProtocol, error) {
	var protocol nativeFlowProtocol

	for _, button := range buttons {
		current := nativeFlowProtocolForButton(button.Name)
		if current == nativeFlowProtocolUnknown {
			return nativeFlowProtocolUnknown, fmt.Errorf(
				"unsupported native flow button: %s",
				button.Name,
			)
		}

		if protocol == nativeFlowProtocolUnknown {
			protocol = current
			continue
		}

		if protocol != current {
			return nativeFlowProtocolUnknown, errors.New(
				"native flow contains buttons requiring incompatible protocols",
			)
		}
	}

	return protocol, nil
}

// nativeFlowProtocolForButton contains only protocol mappings that we have
// explicitly verified.
func nativeFlowProtocolForButton(name string) nativeFlowProtocol {
	switch name {
	case "cta_url", "cta_call", "cta_copy", "quick_reply":
		return nativeFlowProtocolMixed

	case "payment_info":
		return nativeFlowProtocolPaymentInfo

	default:
		return nativeFlowProtocolUnknown
	}
}

func buildNativeFlowProtocolNodes(protocol nativeFlowProtocol) []waBinary.Node {
	switch protocol {
	case nativeFlowProtocolMixed:
		return []waBinary.Node{{
			Tag: "biz",
			Content: []waBinary.Node{{
				Tag:   "interactive",
				Attrs: waBinary.Attrs{"type": "native_flow", "v": "1"},
				Content: []waBinary.Node{{
					Tag: "native_flow",
					Attrs: waBinary.Attrs{
						"name": "mixed",
						"v":    "9",
					},
				}},
			}},
		}}

	case nativeFlowProtocolPaymentInfo:
		return []waBinary.Node{{
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

	default:
		return nil
	}
}