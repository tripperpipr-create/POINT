package mcpclient

import (
	"encoding/json"
	"strconv"
)

// message — одно сообщение JSON-RPC 2.0 в любую сторону: запрос (method + id),
// уведомление (method без id) или ответ (id без method).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

func (m message) isResponse() bool     { return m.Method == "" && len(m.ID) > 0 }
func (m message) isRequest() bool      { return m.Method != "" && len(m.ID) > 0 }
func (m message) isNotification() bool { return m.Method != "" && len(m.ID) == 0 }

// idKey приводит id к строке-ключу: сервер обязан вернуть тот же id, но может
// записать число строкой — сверяем по смыслу, а не по байтам.
func idKey(raw json.RawMessage) string {
	var number int64
	if err := json.Unmarshal(raw, &number); err == nil {
		return strconv.FormatInt(number, 10)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

func request(id int64, method string, params any) (message, error) {
	msg := message{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return message{}, err
		}
		msg.Params = raw
	}
	return msg, nil
}

func notification(method string, params any) (message, error) {
	msg := message{JSONRPC: "2.0", Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return message{}, err
		}
		msg.Params = raw
	}
	return msg, nil
}

func resultFor(id json.RawMessage, result any) message {
	raw, _ := json.Marshal(result)
	return message{JSONRPC: "2.0", ID: id, Result: raw}
}

func errorFor(id json.RawMessage, code int, text string) message {
	return message{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: text}}
}
